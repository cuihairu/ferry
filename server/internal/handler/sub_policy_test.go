package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// decoded unpacks a v2ray subscription body (base64 text).
func decoded(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rec.Body.String()))
	if err != nil {
		t.Fatalf("body not base64: %v (%s)", err, rec.Body)
	}
	return string(raw)
}

// TestSubscriptionPolicyFilter covers the delivery-side enforcement of
// F-0288/F-0291 (region + health constraints) and F-0286 (compact output):
// a user-scope policy trims the entry list the subscription serves.
func TestSubscriptionPolicyFilter(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "polsub", "quota_bytes": 1000000, "expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	user := mustJSON(t, rec)
	uid := uint(user["id"].(float64))
	token := user["sub_token"].(string)

	// Two entry nodes: jp-online (allowed) and hk-offline (filtered by both
	// the region whitelist and the health gate).
	mkNode := func(name, region, status string) {
		rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
			"config": `{"uuid":"u-1","tls":true,"sni":"s.com","net":"ws","path":"/wss","host":"cdn.example.com"}`,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create node %s: %d %s", name, rec.Code, rec.Body)
		}
		if err := db.Model(&storage.Node{}).Where("name = ?", name).
			Updates(map[string]any{
				"role": "entry", "meta_init": true, "region": region, "status": status,
			}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mkNode("jp-online", "JP", "online")
	mkNode("hk-offline", "HK", "offline")

	// User policy: regions=JP, min_health=healthy.
	rec = doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope": "user", "subject_id": uid,
		"constraints": map[string]any{"regions": []string{"JP"}, "min_health": "healthy"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create policy: %d %s", rec.Code, rec.Body)
	}

	body := getSub(t, r, "/sub/"+token, "v2rayNG/1.8")
	if body.Code != http.StatusOK {
		t.Fatalf("sub fetch: %d %s", body.Code, body.Body)
	}
	raw := decoded(t, body)
	if strings.Contains(raw, "hk-offline.example.com") {
		t.Fatalf("policy-violating node leaked into subscription: %s", raw)
	}
	if !strings.Contains(raw, "jp-online.example.com") {
		t.Fatalf("allowed node missing from subscription: %s", raw)
	}

	// No policy on the chain (delete it): both nodes come back.
	var pol storage.Policy
	if err := db.Where("scope = ?", "user").First(&pol).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "DELETE", "/api/policies/"+utoa(pol.ID), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete policy: %d %s", rec.Code, rec.Body)
	}
	body = getSub(t, r, "/sub/"+token, "v2rayNG/1.8")
	if !strings.Contains(decoded(t, body), "hk-offline.example.com") {
		t.Fatalf("unconstrained subscription missing hk node: %s", body.Body)
	}
}

// TestSubscriptionGroupDelivery covers F-0270's delivery side: a policy chain
// pinning a path group serves only the resolved serving path's entry node --
// a degraded default loses to the healthy candidate -- and removing the pin
// restores the full list.
func TestSubscriptionGroupDelivery(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "grpsub", "quota_bytes": 1000000, "expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	token := mustJSON(t, rec)["sub_token"].(string)

	mkNode := func(name, role string) storage.Node {
		n := storage.Node{Name: name, Token: name, Address: name + ".example.com", Port: 443,
			Protocol: "vless", Config: `{"uuid":"u-1","tls":true,"sni":"s.com","net":"ws","path":"/wss","host":"cdn.example.com"}`,
			Role: role, Enabled: true, Status: "online", PoolState: "active"}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	entryA := mkNode("gentry-a", "entry")
	entryB := mkNode("gentry-b", "entry")
	landA := mkNode("gland-a", "landing")
	landB := mkNode("gland-b", "landing")

	pathA := storage.Path{Name: "gpath-a", EntryNodeID: entryA.ID, LandingNodeID: landA.ID}
	pathB := storage.Path{Name: "gpath-b", EntryNodeID: entryB.ID, LandingNodeID: landB.ID}
	if err := db.Create(&pathA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&pathB).Error; err != nil {
		t.Fatal(err)
	}
	group := storage.PathGroup{Name: "gsub", Direction: "out", DefaultPathID: &pathA.ID}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	for _, m := range []storage.PathGroupMember{
		{GroupID: group.ID, PathID: pathA.ID, Position: 0},
		{GroupID: group.ID, PathID: pathB.ID, Position: 1},
	} {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
	// The default path's landing probes sick; the candidate's is healthy.
	now := time.Now()
	verdict := func(prober, target uint, v string) {
		if err := db.Create(&storage.ProbeReport{
			NodeID: prober, TargetKind: "peer", TargetNodeID: &target,
			Verdict: v, ProbedAt: now.Add(-time.Minute),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	verdict(entryA.ID, landA.ID, "sick")
	verdict(entryB.ID, landB.ID, "healthy")

	// System policy pins the group.
	rec = doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope":       "system",
		"constraints": map[string]any{"path_group_id": group.ID},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create policy: %d %s", rec.Code, rec.Body)
	}

	body := getSub(t, r, "/sub/"+token, "v2rayNG/1.8")
	if body.Code != http.StatusOK {
		t.Fatalf("sub fetch: %d %s", body.Code, body.Body)
	}
	raw := decoded(t, body)
	if strings.Contains(raw, "gentry-a.example.com") {
		t.Fatalf("degraded default leaked into subscription: %s", raw)
	}
	if !strings.Contains(raw, "gentry-b.example.com") {
		t.Fatalf("healthy candidate missing from subscription: %s", raw)
	}

	// Removing the pin restores the full entry list.
	var pol storage.Policy
	if err := db.Where("scope = ?", "system").First(&pol).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "DELETE", "/api/policies/"+utoa(pol.ID), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete policy: %d %s", rec.Code, rec.Body)
	}
	body = getSub(t, r, "/sub/"+token, "v2rayNG/1.8")
	raw = decoded(t, body)
	if !strings.Contains(raw, "gentry-a.example.com") || !strings.Contains(raw, "gentry-b.example.com") {
		t.Fatalf("unpinned subscription missing entries: %s", raw)
	}
}

// TestSubXPolicyAndGroupParity covers the custom subscription surface's
// parity with the main one (F-1143/F-1144): the scoped policy chain filters
// the candidates and a path group pin serves only the resolved path's entry,
// the same semantics as TestSubscriptionPolicyFilter /
// TestSubscriptionGroupDelivery.
func TestSubXPolicyAndGroupParity(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	panel := panelUserOf(t, r, "xparity-panel", nil)
	rec := doJSONToken(t, r, panel, "POST", "/api/subscriptions", map[string]any{"name": "parity"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create sub: %d %s", rec.Code, rec.Body)
	}
	var s storage.Subscription
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	var owner storage.User
	if err := db.Where("username = ?", "xparity-panel").First(&owner).Error; err != nil {
		t.Fatal(err)
	}

	mkNode := func(name string) storage.Node {
		n := storage.Node{Name: name, Token: name, Address: name + ".example.com", Port: 443,
			Protocol: "vless", Config: `{"uuid":"u-1"}`, Role: "entry", Enabled: true,
			Status: "online", PoolState: "active"}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	entryA := mkNode("xentry-a")
	entryB := mkNode("xentry-b")
	landA := mkNode("xland-a")
	landB := mkNode("xland-b")

	pathA := storage.Path{Name: "xpath-a", EntryNodeID: entryA.ID, LandingNodeID: landA.ID}
	pathB := storage.Path{Name: "xpath-b", EntryNodeID: entryB.ID, LandingNodeID: landB.ID}
	if err := db.Create(&pathA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&pathB).Error; err != nil {
		t.Fatal(err)
	}
	group := storage.PathGroup{Name: "xparity-group", Direction: "out", DefaultPathID: &pathA.ID}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	for _, m := range []storage.PathGroupMember{
		{GroupID: group.ID, PathID: pathA.ID, Position: 0},
		{GroupID: group.ID, PathID: pathB.ID, Position: 1},
	} {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Both path landings healthy: the default path wins the pin.
	now := time.Now()
	verdict := func(prober, target uint, v string) {
		if err := db.Create(&storage.ProbeReport{
			NodeID: prober, TargetKind: "peer", TargetNodeID: &target,
			Verdict: v, ProbedAt: now.Add(-time.Minute),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	verdict(entryA.ID, landA.ID, "healthy")
	verdict(entryB.ID, landB.ID, "healthy")

	// The subscription owner's user policy pins the group.
	rec = doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope": "user", "subject_id": owner.ID,
		"constraints": map[string]any{"path_group_id": group.ID},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create policy: %d %s", rec.Code, rec.Body)
	}

	body := getSub(t, r, "/sub/x/"+s.Token, "v2rayNG/1.8")
	if body.Code != http.StatusOK {
		t.Fatalf("sub fetch: %d %s", body.Code, body.Body)
	}
	raw := decoded(t, body)
	if !strings.Contains(raw, "xentry-a.example.com") || strings.Contains(raw, "xentry-b.example.com") {
		t.Fatalf("group pin not honored on the custom surface: %s", raw)
	}

	// Removing the pin restores both entries.
	var pol storage.Policy
	if err := db.Where("scope = ?", "user").First(&pol).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "DELETE", "/api/policies/"+utoa(pol.ID), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete policy: %d %s", rec.Code, rec.Body)
	}
	raw = decoded(t, getSub(t, r, "/sub/x/"+s.Token, "v2rayNG/1.8"))
	if !strings.Contains(raw, "xentry-a.example.com") || !strings.Contains(raw, "xentry-b.example.com") {
		t.Fatalf("unpinned custom subscription missing entries: %s", raw)
	}
}
