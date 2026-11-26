package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPathCandidatePreview covers F-1015: GET /api/paths/preview lists the
// landing candidates the sweep would consider for an entry, ranked by the
// configured policy, with eligibility from capability, probe and node health.
// Nothing is written -- the preview has no Path row side effect.
func TestPathCandidatePreview(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mkNode := func(name, role, direction, region, isp string, caps []string) uint {
		n := storage.Node{
			Name: name, Address: name + ".example.com", Port: 443, Protocol: "vless",
			Token: name, Enabled: true, Status: "online", PoolState: "active",
			Role: role, Direction: direction, Region: region, ISP: isp,
			Capabilities: storage.MustJSON(caps),
		}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	entry := mkNode("pv-entry", "entry", "out", "华东", "电信",
		[]string{"proxy_ingress", "relay_entry", "probe"})
	cheap := mkNode("pv-cheap", "landing", "out", "华东", "电信", []string{"relay_landing"})
	cheapNode := storage.Node{}
	if err := db.First(&cheapNode, cheap).Error; err != nil {
		t.Fatal(err)
	}
	cheapNode.BillingType = "按流量"
	cheapNode.TrafficPriceCents = 5
	cheapNode.UpdatedAt = time.Now()
	if err := db.Save(&cheapNode).Error; err != nil {
		t.Fatal(err)
	}
	pricey := mkNode("pv-pricey", "landing", "out", "华东", "电信", []string{"relay_landing"})
	priceyNode := storage.Node{}
	if err := db.First(&priceyNode, pricey).Error; err != nil {
		t.Fatal(err)
	}
	priceyNode.BillingType = "按流量"
	priceyNode.TrafficPriceCents = 50
	priceyNode.UpdatedAt = time.Now()
	if err := db.Save(&priceyNode).Error; err != nil {
		t.Fatal(err)
	}
	wrongCap := mkNode("pv-nocap", "landing", "out", "华东", "电信", []string{"probe"})
	mkNode("pv-nodir", "landing", "in", "华东", "电信", []string{"relay_landing"})

	// A healthy probe verdict on the cheap landing so nodehealth can call it healthy.
	db.Create(&storage.ProbeReport{
		NodeID: cheap, TargetKind: agentproto.ProbeTargetExit, TargetNodeID: &cheap,
		Verdict: agentproto.ProbeVerdictHealthy, ProbedAt: time.Now(),
	})

	rec := doJSON(t, r, "GET", "/api/paths/preview?entry_node_id="+utoa(entry), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	var out pathPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.EntryNodeID != entry || out.Direction != "out" || out.Policy != "cost_first" {
		t.Fatalf("header = %+v", out)
	}
	if out.WinnerID != cheap {
		t.Fatalf("winner = %d, want cheap %d; candidates = %+v", out.WinnerID, cheap, out.Candidates)
	}
	if len(out.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2 (wrong cap/direction filtered): %+v", len(out.Candidates), out.Candidates)
	}
	for _, c := range out.Candidates {
		if c.NodeID == wrongCap || c.NodeID == 0 {
			t.Fatalf("unexpected candidate: %+v", c)
		}
	}
	// cost_first ranks the cheap landing first; both are eligible given a
	// healthy verdict on the cheap node and no verdict on the pricey one (no
	// verdict = unknown, which the node health gate still accepts).
	if out.Candidates[0].NodeID != cheap || !out.Candidates[0].Eligible {
		t.Fatalf("rank order = %+v", out.Candidates)
	}
	if out.Candidates[0].CostPerGB != 5 || out.Candidates[1].CostPerGB != 50 {
		t.Fatalf("costs = %+v", out.Candidates)
	}

	// Preview does not write a path.
	var paths int64
	if err := db.Model(&storage.Path{}).Count(&paths).Error; err != nil || paths != 0 {
		t.Fatalf("preview wrote paths: count=%d err=%v", paths, err)
	}

	// Bad input.
	if rec = doJSON(t, r, "GET", "/api/paths/preview", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing entry = %d", rec.Code)
	}
	if rec = doJSON(t, r, "GET", "/api/paths/preview?entry_node_id=9999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing node = %d", rec.Code)
	}
	if rec = doJSON(t, r, "GET", "/api/paths/preview?entry_node_id="+utoa(entry)+"&direction=sideways", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad direction = %d", rec.Code)
	}
}
