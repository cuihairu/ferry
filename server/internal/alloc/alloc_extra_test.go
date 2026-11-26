package alloc

import (
	"testing"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPickTieBreaks covers lessFor's tie-break fallbacks per policy (name lexicographic/premium line/cost).
func TestPickTieBreaks(t *testing.T) {
	w := DefaultPolicySetting().Weights()
	// least_conn: load and cost all equal → name lexicographic order.
	cands := []Candidate{
		{Node: storage.Node{Name: "b", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 5, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{Name: "a", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 5, Healthy: true, CostKnown: true, CostFresh: true},
	}
	idx, _ := Pick(PolicyLeastConn, "out", MissingCostDeprioritize, w, cands)
	if cands[idx].Node.Name != "a" {
		t.Fatalf("least_conn tie name: %s", cands[idx].Node.Name)
	}
	// cost_first: cost equal → the lower load wins.
	cands = []Candidate{
		{Node: storage.Node{Name: "busy", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 50, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{Name: "idle", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 5, Healthy: true, CostKnown: true, CostFresh: true},
	}
	idx, _ = Pick(PolicyCostFirst, "out", MissingCostDeprioritize, w, cands)
	if cands[idx].Node.Name != "idle" {
		t.Fatalf("cost_first load tie: %s", cands[idx].Node.Name)
	}
	// perf_first: a premium line (in direction) beats a non-premium even with a higher load.
	cands = []Candidate{
		{Node: storage.Node{Name: "premium", BwDownMbps: 10, LineType: "cn2_gia"}, Conns: 100, Healthy: true},
		{Node: storage.Node{Name: "plain", BwDownMbps: 10}, Conns: 1, Healthy: true},
	}
	idx, _ = Pick(PolicyPerfFirst, agentproto.DirectionIn, MissingCostDeprioritize, w, cands)
	if cands[idx].Node.Name != "premium" {
		t.Fatalf("perf_first premium: %s", cands[idx].Node.Name)
	}
	// perf_first non-premium with equal load → the lower cost wins.
	cands = []Candidate{
		{Node: storage.Node{Name: "cheap", BwDownMbps: 10, TrafficPriceCents: 1, BillingType: "按流量"}, Conns: 5, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{Name: "pricey", BwDownMbps: 10, TrafficPriceCents: 9, BillingType: "按流量"}, Conns: 5, Healthy: true, CostKnown: true, CostFresh: true},
	}
	idx, _ = Pick(PolicyPerfFirst, "out", MissingCostDeprioritize, w, cands)
	if cands[idx].Node.Name != "cheap" {
		t.Fatalf("perf_first cost: %s", cands[idx].Node.Name)
	}
	// balanced: load and cost all equal → name lexicographic order.
	cands = []Candidate{
		{Node: storage.Node{Name: "z", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 5, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{Name: "y", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 5, Healthy: true, CostKnown: true, CostFresh: true},
	}
	idx, _ = Pick(PolicyBalanced, "out", MissingCostDeprioritize, w, cands)
	if cands[idx].Node.Name != "y" {
		t.Fatalf("balanced tie name: %s", cands[idx].Node.Name)
	}
	// balanced premium subtraction (in direction): the premium line wins.
	cands = []Candidate{
		{Node: storage.Node{Name: "prem", BwDownMbps: 10, LineType: "iplc"}, Conns: 5, Healthy: true},
		{Node: storage.Node{Name: "normal", BwDownMbps: 10}, Conns: 10, Healthy: true},
	}
	idx, _ = Pick(PolicyBalanced, agentproto.DirectionIn, MissingCostDeprioritize, w, cands)
	if cands[idx].Node.Name != "prem" {
		t.Fatalf("balanced premium: %s", cands[idx].Node.Name)
	}
}

// TestSweepSkipsMaintenance covers F-0206: a maintenance node stays out of
// scheduling (entries and landings) until the window ends, even while active
// and healthy.
func TestSweepSkipsMaintenance(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "maint-a", "entry", "out", nil)
	seedNode(t, db, "maint-land", "landing", "out", nil)
	seedNode(t, db, "open-land", "landing", "out", nil)
	// Only one landing is under maintenance: allocation must pick the healthy one.
	var land storage.Node
	if err := db.Where("name = ?", "maint-land").First(&land).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.Node{}).Where("id = ?", land.ID).Update("maintenance", true).Error; err != nil {
		t.Fatal(err)
	}

	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != "open-land" {
		t.Fatalf("sweep with maintenance landing events = %+v", events)
	}

	// Put the entry under maintenance too: nothing is assignable.
	var ent storage.Node
	if err := db.Where("name = ?", "maint-a").First(&ent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.Node{}).Where("id = ?", ent.ID).Update("maintenance", true).Error; err != nil {
		t.Fatal(err)
	}
	// Release the current assignment so a fresh pick would be needed.
	if err := db.Where("1 = 1").Delete(&storage.LandingAssignment{}).Error; err != nil {
		t.Fatal(err)
	}
	events, err = Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("all-maintenance sweep events = %+v", events)
	}
	var rows int64
	db.Model(&storage.LandingAssignment{}).Count(&rows)
	if rows != 0 {
		t.Fatalf("assignment created from maintenance nodes: %d", rows)
	}
}
