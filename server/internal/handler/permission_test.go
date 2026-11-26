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
