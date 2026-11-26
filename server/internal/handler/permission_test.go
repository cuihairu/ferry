package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCloudAccountPerm covers F-1703: the independent cloud-account permission —
// an operator is 403 on provider mutations, the grant (closed vocabulary,
// super-only PUT) opens exactly that surface, and revoking closes it again.
// Reads stay open to any admin.
func TestCloudAccountPerm(t *testing.T) {
	// A master key: the granted leg actually seals a provider credential.
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })

	// The operator identity behind opJWT's fixed claims.ID.
	if err := db.Create(&storage.User{ID: 999, Username: "op-perm", SubToken: "st-op-perm",
		Password: "x", Enabled: true, IsAdmin: true, AdminRole: "operator"}).Error; err != nil {
		t.Fatal(err)
	}
	op := opJWT(t, "op-perm")
	body := map[string]any{"name": "vultr", "type": "vultr", "access_key": "sk-live"}

	// No grant: mutation 403, read fine.
	if rec := doPanel(t, r, op, "POST", "/api/providers", body); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "missing permission cloud.account.manage") {
		t.Fatalf("ungranted create: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, op, "GET", "/api/providers", nil); rec.Code != http.StatusOK {
		t.Fatalf("ungranted list: %d", rec.Code)
	}

	// The perms surface itself is super-only.
	if rec := doPanel(t, r, op, "GET", "/api/admins/999/perms", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("operator perms read: %d", rec.Code)
	}
	if rec := doPanel(t, r, op, "PUT", "/api/admins/999/perms", map[string]any{"perms": []string{}}); rec.Code != http.StatusForbidden {
		t.Fatalf("operator perms write: %d", rec.Code)
	}

	// Super grants: unknown names reject the whole write.
	if rec := doJSON(t, r, "PUT", "/api/admins/999/perms", map[string]any{"perms": []string{"cloud.account.manage", "bogus.perm"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown perm: %d %s", rec.Code, rec.Body)
	}
	rec := doJSON(t, r, "PUT", "/api/admins/999/perms", map[string]any{"perms": []string{PermCloudAccount}})
	if rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}
	var rows []storage.AdminPerm
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Perm != PermCloudAccount || rows[0].GrantedBy == "" {
		t.Fatalf("grant rows = %+v", rows)
	}

	// Granted: the mutation passes and lands a provider.
	rec = doPanel(t, r, op, "POST", "/api/providers", body)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("granted create: %d %s", rec.Code, rec.Body)
	}
	// Exactly one provider must exist: the ungranted attempt must not have
	// leaked a create past the denial (a fail-without-abort middleware would).
	var provs []storage.Provider
	if err := db.Find(&provs).Error; err != nil {
		t.Fatal(err)
	}
	if len(provs) != 1 || provs[0].Name != "vultr" {
		t.Fatalf("providers = %+v", provs)
	}

	// Revoke (empty set): the mutation is closed again.
	if rec := doJSON(t, r, "PUT", "/api/admins/999/perms", map[string]any{"perms": []string{}}); rec.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, op, "POST", "/api/providers", body); rec.Code != http.StatusForbidden {
		t.Fatalf("revoked create: %d %s", rec.Code, rec.Body)
	}

	// Granting to a non-admin id is a 404.
	if rec := doJSON(t, r, "PUT", "/api/admins/424242/perms", map[string]any{"perms": []string{PermCloudAccount}}); rec.Code != http.StatusNotFound {
		t.Fatalf("grant non-admin: %d", rec.Code)
	}
}

// TestCloudOpPerms covers F-1704/F-1705: the operation-type permission gates —
// power ops (on/off/reboot) need cloud.power, force-stop/rebuild the
// cloud.highrisk tier; the super role passes unconditionally, and a grant
// opens exactly its own tier (the check rides inside the handler since the
// middleware cannot read the body).
func TestCloudOpPerms(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })

	if err := db.Create(&storage.User{ID: 999, Username: "op-op", SubToken: "st-op-op",
		Password: "x", Enabled: true, IsAdmin: true, AdminRole: "operator"}).Error; err != nil {
		t.Fatal(err)
	}
	op := opJWT(t, "op-op")
	req := func(opType string) map[string]any {
		return map[string]any{"provider_id": 1, "op": opType,
			"specs": []map[string]any{{"resource_id": 1}}}
	}

	// No grants: both tiers 403 with the right perm named.
	rec := doPanel(t, r, op, "POST", "/api/cloud-operations", req("power_on"))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "missing permission cloud.power") {
		t.Fatalf("power_on ungranted: %d %s", rec.Code, rec.Body)
	}
	rec = doPanel(t, r, op, "POST", "/api/cloud-operations", req("force_stop"))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "missing permission cloud.highrisk") {
		t.Fatalf("force_stop ungranted: %d %s", rec.Code, rec.Body)
	}

	// The power grant opens power ops but not the high-risk tier.
	if rec := doJSON(t, r, "PUT", "/api/admins/999/perms", map[string]any{"perms": []string{PermCloudPower}}); rec.Code != http.StatusOK {
		t.Fatalf("grant power: %d %s", rec.Code, rec.Body)
	}
	rec = doPanel(t, r, op, "POST", "/api/cloud-operations", req("power_on"))
	if rec.Code == http.StatusForbidden {
		t.Fatalf("granted power_on still 403: %d %s", rec.Code, rec.Body)
	}
	rec = doPanel(t, r, op, "POST", "/api/cloud-operations", req("force_stop"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("power grant must not open force_stop: %d %s", rec.Code, rec.Body)
	}

	// The high-risk grant opens force_stop past the perm gate (the oper-level
	// confirmation flow then answers on its own terms, not 403).
	if rec := doJSON(t, r, "PUT", "/api/admins/999/perms", map[string]any{"perms": []string{PermCloudPower, PermCloudHighRisk}}); rec.Code != http.StatusOK {
		t.Fatalf("grant highrisk: %d %s", rec.Code, rec.Body)
	}
	rec = doPanel(t, r, op, "POST", "/api/cloud-operations", req("force_stop"))
	if rec.Code == http.StatusForbidden {
		t.Fatalf("granted force_stop still 403: %d %s", rec.Code, rec.Body)
	}

	// The super role passes without any grant (default admin JWT).
	rec = doJSON(t, r, "POST", "/api/cloud-operations", req("power_on"))
	if rec.Code == http.StatusForbidden {
		t.Fatalf("super power_on 403: %d %s", rec.Code, rec.Body)
	}
}

// TestCostViewPerm covers F-1706: the cost read surface — aggregates, the bill
// ledger and the CSV export — needs the cost.view grant; writes (thresholds,
// rules, imports) are deliberately not part of this tier. The export reuses
// the list filters.
func TestCostViewPerm(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })

	if err := db.Create(&storage.User{ID: 999, Username: "op-cost", SubToken: "st-op-cost",
		Password: "x", Enabled: true, IsAdmin: true, AdminRole: "operator"}).Error; err != nil {
		t.Fatal(err)
	}
	op := opJWT(t, "op-cost")
	if err := db.Create(&storage.CostRecord{CostProviderID: 1, Source: "manual",
		BillID: "bill-1", Currency: "USD", AmountCents: 4200}).Error; err != nil {
		t.Fatal(err)
	}

	// No grant: both reads 403 with the perm named.
	for _, path := range []string{"/api/cost", "/api/cost/records", "/api/cost/records/export"} {
		rec := doPanel(t, r, op, "GET", path, nil)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "missing permission cost.view") {
			t.Fatalf("ungranted %s: %d %s", path, rec.Code, rec.Body)
		}
	}

	// Grant opens the reads; the export streams CSV with the seeded row.
	if rec := doJSON(t, r, "PUT", "/api/admins/999/perms", map[string]any{"perms": []string{PermCostView}}); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, op, "GET", "/api/cost", nil); rec.Code != http.StatusOK {
		t.Fatalf("granted /cost: %d %s", rec.Code, rec.Body)
	}
	rec := doPanel(t, r, op, "GET", "/api/cost/records/export", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("granted export: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "bill-1") ||
		!strings.Contains(rec.Body.String(), "id,cost_provider_id,source") {
		t.Fatalf("export body missing header/row:\n%s", rec.Body)
	}
	// The list filters carry over: a non-matching source yields header only.
	rec = doPanel(t, r, op, "GET", "/api/cost/records/export?source=api", nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "bill-1") {
		t.Fatalf("filtered export: %d %s", rec.Code, rec.Body)
	}

	// The super role passes without any grant.
	if rec := doJSON(t, r, "GET", "/api/cost/records/export", nil); rec.Code != http.StatusOK {
		t.Fatalf("super export: %d %s", rec.Code, rec.Body)
	}

	// Revoke closes the reads again.
	if rec := doJSON(t, r, "PUT", "/api/admins/999/perms", map[string]any{"perms": []string{}}); rec.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, op, "GET", "/api/cost/records/export", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("revoked export: %d %s", rec.Code, rec.Body)
	}
}

// TestCostManagePerm covers F-1707: the cost write surface — thresholds, the
// reference table, watches, sources/imports, allocation rules, estimates,
// lines, reconciliations — needs cost.manage, tiered apart from cost.view
// (the manage grant alone opens writes but not the read surface).
func TestCostManagePerm(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })

	if err := db.Create(&storage.User{ID: 999, Username: "op-costmg", SubToken: "st-op-costmg",
		Password: "x", Enabled: true, IsAdmin: true, AdminRole: "operator"}).Error; err != nil {
		t.Fatal(err)
	}
	op := opJWT(t, "op-costmg")

	// No grant: representative writes 403 with the perm named (the middleware
	// denies before the handler sees the body, so empty bodies are fine here).
	for _, rr := range [][2]string{
		{"PUT", "/api/cost/threshold"}, {"POST", "/api/cost/watches"},
		{"POST", "/api/cost/allocation-rules"}, {"POST", "/api/cost/reconciliations"},
	} {
		rec := doPanel(t, r, op, rr[0], rr[1], map[string]any{})
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "missing permission cost.manage") {
			t.Fatalf("ungranted %s %s: %d %s", rr[0], rr[1], rec.Code, rec.Body)
		}
	}

	// The manage grant alone opens the writes but not the reads: the tiers are
	// independent (neither implies the other).
	if rec := doJSON(t, r, "PUT", "/api/admins/999/perms", map[string]any{"perms": []string{PermCostManage}}); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}
	for _, rr := range [][2]string{
		{"PUT", "/api/cost/threshold"}, {"POST", "/api/cost/watches"},
		{"POST", "/api/cost/allocation-rules"}, {"POST", "/api/cost/estimates/compute"},
		{"PUT", "/api/cost/ref-table"}, {"POST", "/api/cost/lines"},
	} {
		rec := doPanel(t, r, op, rr[0], rr[1], map[string]any{})
		if rec.Code == http.StatusForbidden {
			t.Fatalf("granted %s %s still 403: %d %s", rr[0], rr[1], rec.Code, rec.Body)
		}
	}
	if rec := doPanel(t, r, op, "GET", "/api/cost", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("manage grant must not open reads: %d %s", rec.Code, rec.Body)
	}

	// The super role passes both tiers without any grant.
	if rec := doJSON(t, r, "GET", "/api/cost", nil); rec.Code != http.StatusOK {
		t.Fatalf("super /cost: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "POST", "/api/cost/watches", map[string]any{}); rec.Code == http.StatusForbidden {
		t.Fatalf("super watch create 403: %d %s", rec.Code, rec.Body)
	}
}
