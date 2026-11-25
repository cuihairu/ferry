package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// subEntries fetches the subscription body and decodes base64 (v2ray format) to make the entry list assertable.
func subEntries(t *testing.T, body string) string {
	t.Helper()
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
	if err != nil {
		t.Fatalf("decode sub body: %v", err)
	}
	return string(dec)
}

// TestPoolAPI covers the entry pool API (E-16): the pool list only holds
// entry/both rows plus the latest probe verdict; manual reset only works on
// the suspended state (active 409, missing 404); a suspended node disappears
// from the subscription entry list and comes back after resume.
func TestPoolAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Subscription user + two entries (in-pool / suspended) + one landing (not in the pool)
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "alice", "quota_bytes": 1000}); rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	db.Where("username = ?", "alice").First(&u)

	nodes := []storage.Node{
		{Name: "hk-entry", Address: "hk.example.com", Port: 443, Protocol: "vless", Token: "t-hk", Role: "entry", Enabled: true, Config: `{"uuid":"u"}`},
		{Name: "us-entry", Address: "us.example.com", Port: 443, Protocol: "vless", Token: "t-us", Role: "entry", Enabled: true, Config: `{"uuid":"u"}`},
		{Name: "jp-landing", Address: "jp.example.com", Port: 443, Protocol: "vless", Token: "t-jp", Role: "landing", Enabled: true},
	}
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	target := nodes[1].ID
	if err := db.Create(&storage.ProbeReport{
		NodeID: nodes[0].ID, TargetKind: agentproto.ProbeTargetPeer, TargetNodeID: &target,
		Verdict: agentproto.ProbeVerdictSick, ProbedAt: time.Now().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}

	// ---- Pool list: no landing, us-entry carries the latest sick verdict ----
	rec := doJSON(t, r, "GET", "/api/pool", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list pool: %d %s", rec.Code, rec.Body)
	}
	var views []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("pool rows = %d, want 2 (landing excluded)", len(views))
	}
	byID := map[string]map[string]any{}
	for _, v := range views {
		byID[v["name"].(string)] = v
	}
	if v, ok := byID["us-entry"]; !ok || v["last_verdict"] != "sick" || v["pool_state"] != "active" {
		t.Fatalf("us-entry row = %+v", v)
	}
	if v, ok := byID["hk-entry"]; !ok || v["last_verdict"] != "" {
		t.Fatalf("hk-entry row = %+v", v)
	}

	// ---- Subscription: both entries present ----
	rec = getSub(t, r, "/sub/"+u.SubToken, "v2rayN/1.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("sub: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(subEntries(t, rec.Body.String()), "us.example.com") {
		t.Fatalf("sub should list us-entry before suspend: %s", rec.Body.String())
	}

	// ---- Manual reset on the active state -> 409 ----
	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(target), 10)+"/resume", nil); rec.Code != http.StatusConflict {
		t.Fatalf("resume active should 409, got %d %s", rec.Code, rec.Body)
	}

	// ---- Suspend us-entry -> gone from the subscription ----
	if err := db.Model(&storage.Node{}).Where("id = ?", target).
		Updates(map[string]any{"pool_state": pool.StateSuspended, "pool_reason": "连续 3 次探测 sick"}).Error; err != nil {
		t.Fatal(err)
	}
	rec = getSub(t, r, "/sub/"+u.SubToken, "v2rayN/1.0")
	if strings.Contains(subEntries(t, rec.Body.String()), "us.example.com") {
		t.Fatalf("suspended entry must be dropped from subscription: %s", rec.Body.String())
	}

	// ---- Manual reset -> subscription recovers; missing -> 404 ----
	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(target), 10)+"/resume", nil); rec.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", rec.Code, rec.Body)
	}
	var after storage.Node
	db.First(&after, target)
	if after.PoolState != pool.StateActive || after.PoolReason != "手动复位" {
		t.Fatalf("after resume = %+v", after)
	}
	rec = getSub(t, r, "/sub/"+u.SubToken, "v2rayN/1.0")
	if !strings.Contains(subEntries(t, rec.Body.String()), "us.example.com") {
		t.Fatalf("resumed entry must be back in subscription: %s", rec.Body.String())
	}
	if rec := doJSON(t, r, "POST", "/api/pool/424242/resume", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("resume missing should 404: %d", rec.Code)
	}
}

// TestPoolStandbyAPI covers standby marking (BR-3): marking an in-pool entry
// standby, a repeat mark 409, standby returning to the pool via resume, and a
// non-entry node rejected 400.
func TestPoolStandbyAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if err := db.Create(&storage.Node{Name: "sb-entry", Token: "tok-sb", Role: "entry",
		Enabled: true, Status: "online", PoolState: pool.StateActive}).Error; err != nil {
		t.Fatal(err)
	}
	var n storage.Node
	db.Where("name = ?", "sb-entry").First(&n)
	id := strconv.FormatUint(uint64(n.ID), 10)

	if rec := doJSON(t, r, "POST", "/api/pool/"+id+"/standby", nil); rec.Code != http.StatusOK {
		t.Fatalf("standby: %d %s", rec.Code, rec.Body)
	}
	var after storage.Node
	db.First(&after, n.ID)
	if after.PoolState != pool.StateStandby {
		t.Fatalf("after standby = %+v", after)
	}
	if rec := doJSON(t, r, "POST", "/api/pool/"+id+"/standby", nil); rec.Code != http.StatusConflict {
		t.Fatalf("repeat standby: %d", rec.Code)
	}
	// Standby returns to the pool.
	if rec := doJSON(t, r, "POST", "/api/pool/"+id+"/resume", nil); rec.Code != http.StatusOK {
		t.Fatalf("resume standby: %d %s", rec.Code, rec.Body)
	}
	db.First(&after, n.ID)
	if after.PoolState != pool.StateActive || after.PoolReason != "预备回池" {
		t.Fatalf("after resume = %+v", after)
	}
	// A non-entry node cannot be marked standby.
	landing := storage.Node{Name: "ld", Token: "tok-ld", Role: "landing", Enabled: true}
	if err := db.Create(&landing).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(landing.ID), 10)+"/standby", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("landing standby: %d", rec.Code)
	}
}
