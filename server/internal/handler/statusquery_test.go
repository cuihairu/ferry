package handler

// Status query surface gap batch: dimension-status, transport-status, node traffic and
// config history, and GET /api/token (whoami) had zero test coverage; the loginlog pure-
// function edges (sameNetwork subnet granularity and parse fallback, UA truncation) are
// filled in here too. The verdict logic itself is self-tested by the transport package;
// this file only tests handler-level parameters and shapes.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestDimensionStatusList covers the status light list: ordered by scope,key ascending.
func TestDimensionStatusList(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	base := time.Now()
	rows := []storage.DimensionStatus{
		{Scope: "region", Key: "us", State: "healthy", Since: base, UpdatedAt: base},
		{Scope: "region", Key: "hk", State: "degraded", Reason: "loss", Since: base, UpdatedAt: base},
		{Scope: "isp", Key: "comcast", State: "failed", Reason: "down", Since: base, UpdatedAt: base},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, r, "GET", "/api/dimension-status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("dimension-status: %d %s", rec.Code, rec.Body)
	}
	var out []storage.DimensionStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 3 || out[0].Key != "comcast" || out[1].Key != "hk" || out[2].Key != "us" {
		t.Fatalf("order = %+v, want isp/comcast, region/hk, region/us", out)
	}
	if out[1].State != "degraded" || out[2].State != "healthy" {
		t.Fatalf("states = %+v", out)
	}
}

// TestTransportStatusEndpoint covers the region transport verdict endpoint: empty db
// returns an empty array, window_sec parameter clamping, and a recommendation
// aggregated when fresh verdicts exist inside the window.
func TestTransportStatusEndpoint(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "GET", "/api/transport-status", nil); rec.Code != http.StatusOK {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"10", "4000", "abc"} {
		if rec := doJSON(t, r, "GET", "/api/transport-status?window_sec="+bad, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("window_sec=%s: %d, want 400", bad, rec.Code)
		}
	}
	n := storage.Node{Name: "hk-tls", Token: "tok-hk-tls", Role: "entry", Enabled: true, Region: "hk", Transport: "tls"}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.ProbeReport{
		NodeID: 99, TargetKind: agentproto.ProbeTargetPeer, TargetNodeID: &n.ID,
		Verdict: agentproto.ProbeVerdictHealthy, ProbedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, r, "GET", "/api/transport-status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("seeded: %d %s", rec.Code, rec.Body)
	}
	var rows []struct {
		Region      string `json:"region"`
		Recommended string `json:"recommended"`
		Current     string `json:"current"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 || rows[0].Region != "hk" || rows[0].Recommended != "tls" || rows[0].Current != "tls" {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestNodeTrafficLogsQuery covers node traffic queries: only the given node's rows,
// newest first, limit clamping and invalid ids.
func TestNodeTrafficLogsQuery(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	now := time.Now()
	for _, row := range []storage.NodeTrafficLog{
		{NodeID: 1, Proc: "xray", RxBytes: 10, RecordedAt: now.Add(-2 * time.Minute)},
		{NodeID: 1, Proc: "xray", RxBytes: 20, RecordedAt: now.Add(-time.Minute)},
		{NodeID: 2, Proc: "xray", RxBytes: 30, RecordedAt: now},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	rec := doJSON(t, r, "GET", "/api/nodes/1/traffic-logs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var rows []storage.NodeTrafficLog
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 || rows[0].RxBytes != 20 || rows[1].RxBytes != 10 {
		t.Fatalf("rows = %+v, want node1 newest first", rows)
	}
	if rec = doJSON(t, r, "GET", "/api/nodes/1/traffic-logs?limit=1", nil); rec.Code != http.StatusOK {
		t.Fatalf("limit=1: %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 || rows[0].RxBytes != 20 {
		t.Fatalf("limited rows = %+v", rows)
	}
	for _, bad := range []string{"limit=0", "limit=1001", "limit=abc"} {
		if rec = doJSON(t, r, "GET", "/api/nodes/1/traffic-logs?"+bad, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d, want 400", bad, rec.Code)
		}
	}
	if rec = doJSON(t, r, "GET", "/api/nodes/abc/traffic-logs", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d, want 404", rec.Code)
	}
	if rec = doJSON(t, r, "GET", "/api/nodes/999/traffic-logs", nil); rec.Code != http.StatusOK {
		t.Fatalf("unknown node: %d", rec.Code)
	}
}

// TestNodeConfigsQuery covers config delivery history queries: newest first, limit clamping and invalid ids.
func TestNodeConfigsQuery(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	for _, row := range []storage.NodeConfig{
		{NodeID: 1, Proc: "xray", Version: "v1", Sha256: "aa", Status: "applied"},
		{NodeID: 1, Proc: "xray", Version: "v2", Sha256: "bb", Status: "pending"},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	rec := doJSON(t, r, "GET", "/api/nodes/1/configs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var rows []storage.NodeConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 || rows[0].Version != "v2" || rows[1].Version != "v1" {
		t.Fatalf("rows = %+v, want newest first", rows)
	}
	for _, bad := range []string{"limit=0", "limit=201", "limit=abc"} {
		if rec = doJSON(t, r, "GET", "/api/nodes/1/configs?"+bad, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d, want 400", bad, rec.Code)
		}
	}
	if rec = doJSON(t, r, "GET", "/api/nodes/abc/configs", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d, want 404", rec.Code)
	}
}

// TestGetCurrentUser covers whoami (GET /api/token): admin JWT returns its subject,
// API Token returns admin, no credentials returns 401.
func TestGetCurrentUser(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doJSON(t, r, "GET", "/api/token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin jwt: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Username != "1" {
		t.Fatalf("admin username = %q, want subject 1", out.Username)
	}

	rec = doJSON(t, r, "POST", "/api/token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("issue api token: %d %s", rec.Code, rec.Body)
	}
	var issued struct {
		ApiToken string `json:"api_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil {
		t.Fatalf("decode: %v", err)
	}
	req := httptest.NewRequest("GET", "/api/token", nil)
	req.Header.Set("Authorization", "Bearer "+issued.ApiToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("api token: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Username != "admin" {
		t.Fatalf("api username = %q, want admin", out.Username)
	}
	if rec = doBare(t, r, "GET", "/api/token"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d, want 401", rec.Code)
	}
}

// TestSameNetwork covers subnet verdicts: v4 /24 and v6 /64 granularity, plus parse-failure fallback.
func TestSameNetwork(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"10.1.0.1", "10.1.0.200", true},                  // same /24
		{"10.1.0.1", "10.2.0.1", false},                   // across /24
		{"2001:db8:1:1::1", "2001:db8:1:1:ffff::1", true}, // same /64
		{"2001:db8:1:1::1", "2001:db8:1:2::1", false},     // across /64
		{"10.1.0.1", "2001:db8::1", false},                // v4 vs v6
		{"abc", "abc", true},                              // parse failure falls back to whole string
		{"abc", "abd", false},
	}
	for _, c := range cases {
		if got := sameNetwork(c.a, c.b); got != c.want {
			t.Fatalf("sameNetwork(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestLoginLogUALimit covers UA truncation: user agents over 255 chars are truncated in the db, without affecting login.
func TestLoginLogUALimit(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdmin(t, db, "root", "pw-root")
	req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(`{"username":"root","password":"pw-root"}`))
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", strings.Repeat("x", 300))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	var row storage.LoginLog
	if err := db.Where("ok = ?", true).Order("id DESC").First(&row).Error; err != nil {
		t.Fatalf("log row: %v", err)
	}
	if len(row.UA) != 255 {
		t.Fatalf("ua len = %d, want 255", len(row.UA))
	}
}
