package handler

// Admin list/helper gap batch: the five read-only endpoints dns-providers,
// dns-fronts, probe-reports, quota-link, rulelib and the two pure functions
// NormalizeAdminRoles/checkImportQuota had zero coverage before; the write
// paths (update/delete/create) already have their own cases.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestNormalizeAdminRoles covers legacy role normalization: a role-less admin
// gets super, a sub-admin (operator) is left alone, and it is idempotent.
func TestNormalizeAdminRoles(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	users := []storage.User{
		{Username: "legacy-admin", SubToken: "t1", IsAdmin: true, AdminRole: ""},
		{Username: "operator", SubToken: "t2", IsAdmin: true, AdminRole: "operator"},
		{Username: "normal", SubToken: "t3"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	if err := NormalizeAdminRoles(db); err != nil {
		t.Fatal(err)
	}
	var admin, op storage.User
	if err := db.Where("username=?", "legacy-admin").First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if admin.AdminRole != "super" {
		t.Fatalf("legacy admin role = %q; want super", admin.AdminRole)
	}
	if err := db.Where("username=?", "operator").First(&op).Error; err != nil {
		t.Fatal(err)
	}
	if op.AdminRole != "operator" {
		t.Fatalf("operator role = %q; want untouched operator", op.AdminRole)
	}
	// idempotent: a second run does not error
	if err := NormalizeAdminRoles(db); err != nil {
		t.Fatalf("second normalize: %v", err)
	}
}

// TestCheckImportQuota covers batch import quota checks: missing operator,
// 0 = unlimited, under quota passes, over quota rejects the whole batch.
func TestCheckImportQuota(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	if err := checkImportQuota(db, "ghost", 5); err == nil {
		t.Fatal("missing operator should error")
	}
	op := storage.User{Username: "op", SubToken: "top", IsAdmin: true, AdminRole: "operator"}
	if err := db.Create(&op).Error; err != nil {
		t.Fatal(err)
	}
	if err := checkImportQuota(db, "op", 1); err != nil {
		t.Fatalf("unlimited (max=0): %v", err)
	}
	op.MaxUsers = 3
	if err := db.Save(&op).Error; err != nil {
		t.Fatal(err)
	}
	if err := checkImportQuota(db, "op", 3); err != nil {
		t.Fatalf("under quota: %v", err)
	}
	if err := checkImportQuota(db, "op", 4); err == nil {
		t.Fatal("over quota should error")
	}
}

// TestListDNSProviders covers the DNS provider list: ascending by id, and
// HasAPIKey reports presence only, never exposing the credential itself.
func TestListDNSProviders(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	providers := []storage.DNSProvider{
		{Name: "cf", Type: "cloudflare", APIKey: "sealed-key", Enabled: true},
		{Name: "dnspod", Type: "dnspod", Enabled: false},
	}
	if err := db.Create(&providers).Error; err != nil {
		t.Fatal(err)
	}
	// GORM default:true swallows the zero value: flip it explicitly after create.
	if err := db.Model(&storage.DNSProvider{}).Where("name=?", "dnspod").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, r, "GET", "/api/dns-providers", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("dns-providers: %d %s", rec.Code, rec.Body)
	}
	var out []struct {
		ID        uint   `json:"id"`
		Name      string `json:"name"`
		Type      string `json:"type"`
		Enabled   bool   `json:"enabled"`
		HasAPIKey bool   `json:"has_api_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 || out[0].Name != "cf" || out[1].Name != "dnspod" {
		t.Fatalf("order = %+v", out)
	}
	if !out[0].HasAPIKey || out[1].HasAPIKey {
		t.Fatalf("has_api_key = %+v", out)
	}
	if out[0].Type != "cloudflare" || !out[0].Enabled || out[1].Enabled {
		t.Fatalf("shape = %+v", out)
	}
}

// TestListDNSFronts covers the DNS front domain list: the original rows back
// in ascending id order.
func TestListDNSFronts(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	fronts := []storage.DNSFront{
		{Name: "备用入口", Domain: "backup.example.com", ProviderID: 1,
			PrimaryIP: "1.1.1.1", BackupIPs: `["2.2.2.2"]`},
		{Name: "主入口", Domain: "edge.example.com", ProviderID: 1,
			PrimaryIP: "3.3.3.3", BackupIPs: `[]`},
	}
	if err := db.Create(&fronts).Error; err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, r, "GET", "/api/dns-fronts", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("dns-fronts: %d %s", rec.Code, rec.Body)
	}
	var out []storage.DNSFront
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 || out[0].Domain != "backup.example.com" || out[1].Domain != "edge.example.com" {
		t.Fatalf("rows = %+v", out)
	}
	if out[0].BackupIPs != `["2.2.2.2"]` || out[0].PrimaryIP != "1.1.1.1" {
		t.Fatalf("row shape = %+v", out[0])
	}
}

// TestListProbeReports covers the probe verdict evidence list: newest verdicts
// first, limit clamping (illegal and out of range -> 400), and an empty DB
// returning an empty array.
func TestListProbeReports(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "GET", "/api/probe-reports", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}
	nodeID := uint(1)
	reports := []storage.ProbeReport{
		{NodeID: 99, TargetKind: "peer", TargetNodeID: &nodeID, Verdict: "healthy", ProbedAt: time.Now().Add(-time.Minute)},
		{NodeID: 99, TargetKind: "peer", TargetNodeID: &nodeID, Verdict: "sick", ProbedAt: time.Now()},
	}
	if err := db.Create(&reports).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/probe-reports", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("seeded: %d %s", rec.Code, rec.Body)
	}
	var out []storage.ProbeReport
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 || out[0].Verdict != "sick" || out[1].Verdict != "healthy" {
		t.Fatalf("order = %+v, want sick first", out)
	}
	for _, q := range []string{"0", "1001", "abc"} {
		if rec = doJSON(t, r, "GET", "/api/probe-reports?limit="+q, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%s: %d, want 400", q, rec.Code)
		}
	}
}

// TestListQuotaLink covers the quota linkage list: the setting, downgrade
// traces (active ones first) and the username mapping.
func TestListQuotaLink(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	user := storage.User{Username: "quota-u", SubToken: "tq"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	released := time.Now()
	actions := []storage.QuotaAction{
		{UserID: user.ID, Reason: "超限降档", ReleasedAt: nil},
		{UserID: user.ID, Reason: "手动恢复", ReleasedAt: &released},
	}
	if err := db.Create(&actions).Error; err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, r, "GET", "/api/quota-link", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("quota-link: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Setting interface{} `json:"setting"`
		Rows    []struct {
			ID         uint       `json:"id"`
			Reason     string     `json:"reason"`
			ReleasedAt *time.Time `json:"released_at"`
		} `json:"rows"`
		Names map[uint]string `json:"names"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Rows) != 2 {
		t.Fatalf("rows = %+v", out)
	}
	// active rows (released_at IS NULL) come first, with the username mapping
	if out.Rows[0].ReleasedAt != nil || out.Rows[1].ReleasedAt == nil {
		t.Fatalf("order = %+v, want active first", out.Rows)
	}
	if out.Names[user.ID] != "quota-u" {
		t.Fatalf("names = %+v", out.Names)
	}
}

// TestListRuleLib covers the rulelib version history: id DESC, only rulelib
// snapshots, and errors on a bad id or out-of-range limit.
func TestListRuleLib(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	node := storage.Node{Name: "rule-node", Token: "tok-rule", Role: "entry", Enabled: true}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	rows := []storage.NodeConfig{
		{NodeID: node.ID, Proc: "xray", Kind: "rulelib:direct", Version: "v2", Sha256: "sha2", Status: "applied"},
		{NodeID: node.ID, Proc: "xray", Kind: "rulelib:reject", Version: "v1", Sha256: "sha1", Status: "applied"},
		{NodeID: node.ID, Proc: "xray", Kind: "config:node", Version: "v9", Sha256: "sha9", Status: "applied"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, r, "GET", "/api/nodes/"+strconv.Itoa(int(node.ID))+"/rulelib", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rulelib: %d %s", rec.Code, rec.Body)
	}
	var out []struct {
		Kind    string `json:"kind"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 || out[0].Version != "v1" || out[1].Version != "v2" {
		t.Fatalf("rows = %+v, want rulelib only and id DESC", out)
	}
	if rec = doJSON(t, r, "GET", "/api/nodes/rulelib", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad node id: %d, want 404", rec.Code)
	}
	rec = doJSON(t, r, "GET", "/api/nodes/"+strconv.Itoa(int(node.ID))+"/rulelib?limit=0", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=0: %d, want 400", rec.Code)
	}
}
