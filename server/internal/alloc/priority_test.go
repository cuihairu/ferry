package alloc

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPickPriorityTier covers the comparator rule (F-1029): the priority
// tier outranks every policy metric -- a cheaper or less loaded candidate in
// a worse tier never beats a higher tier; within one tier the policy order
// decides as before. All-zero priorities keep the historical order.
func TestPickPriorityTier(t *testing.T) {
	// tier1: cheap; tier2: expensive. cost_first would love the cheap one,
	// but the tier rule must overrule it.
	tier1 := Candidate{Node: storage.Node{ID: 1, Name: "tier1", SchedPriority: 2,
		BillingType: "按流量", TrafficPriceCents: 90, UpdatedAt: testNow}, Healthy: true, CostKnown: true, CostFresh: true}
	tier2 := Candidate{Node: storage.Node{ID: 2, Name: "tier2", SchedPriority: 1,
		BillingType: "按流量", TrafficPriceCents: 5, UpdatedAt: testNow}, Healthy: true, CostKnown: true, CostFresh: true}
	w := ScoreWeights{Load: 0.5, Cost: 0.3, Premium: 0.2}
	cands := []Candidate{tier1, tier2}
	idx, ok := Pick(PolicyCostFirst, "out", MissingCostDeprioritize, w, cands)
	if !ok || cands[idx].Node.ID != 2 {
		t.Fatalf("cost_first winner = %v, want tier2 despite the higher price", idx)
	}
	// the rendered order follows the same partition: better tier first
	less := LessFor(PolicyCostFirst, "out", MissingCostDeprioritize, w, cands)
	if !less(1, 0) || less(0, 1) {
		t.Fatalf("tier order wrong: less(1,0)=%v less(0,1)=%v", less(1, 0), less(0, 1))
	}

	// within one tier the policy metric still decides
	same := []Candidate{
		{Node: storage.Node{ID: 3, Name: "a", SchedPriority: 1, BillingType: "按流量", TrafficPriceCents: 90, UpdatedAt: testNow}, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{ID: 4, Name: "b", SchedPriority: 1, BillingType: "按流量", TrafficPriceCents: 5, UpdatedAt: testNow}, Healthy: true, CostKnown: true, CostFresh: true},
	}
	idx, ok = Pick(PolicyCostFirst, "out", MissingCostDeprioritize, w, same)
	if !ok || same[idx].Node.ID != 4 {
		t.Fatalf("within-tier winner = %v, want the cheap b", idx)
	}

	// all-zero priorities (the default) behave exactly as before
	zero := []Candidate{
		{Node: storage.Node{ID: 5, Name: "a", BillingType: "按流量", TrafficPriceCents: 90, UpdatedAt: testNow}, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{ID: 6, Name: "b", BillingType: "按流量", TrafficPriceCents: 5, UpdatedAt: testNow}, Healthy: true, CostKnown: true, CostFresh: true},
	}
	idx, ok = Pick(PolicyCostFirst, "out", MissingCostDeprioritize, w, zero)
	if !ok || zero[idx].Node.ID != 6 {
		t.Fatalf("zero-priority winner = %v, want the cheap b", idx)
	}
}

// TestShouldSwitchPriorityTier covers the peak margin rule (F-1029): moving
// into a better tier is always a proven improvement at peak, and moving to a
// worse tier is never allowed there (off-peak rebalancing handles the rest).
func TestShouldSwitchPriorityTier(t *testing.T) {
	w := ScoreWeights{Load: 0.5, Cost: 0.3, Premium: 0.2}
	// current: tier 1 cheap; winner candidate: tier 2 pricey
	cands := []Candidate{
		{Node: storage.Node{ID: 1, Name: "cur", SchedPriority: 1, BillingType: "按流量", TrafficPriceCents: 5, UpdatedAt: testNow}},
		{Node: storage.Node{ID: 2, Name: "win", SchedPriority: 2, BillingType: "按流量", TrafficPriceCents: 50, UpdatedAt: testNow}, Healthy: true, CostKnown: true, CostFresh: true},
	}
	// cur(0) is already in the best tier: a worse-tier winner must not switch
	if shouldSwitch(PolicyCostFirst, "out", MissingCostDeprioritize, w, cands, 0, 1) {
		t.Fatalf("downgrade to a worse tier must wait for off-peak")
	}
	// reversed: current sits in the worse tier, a better tier wins outright
	if !shouldSwitch(PolicyCostFirst, "out", MissingCostDeprioritize, w, cands, 1, 0) {
		t.Fatalf("a better tier is a proven improvement, margins do not apply")
	}
	// same tier: the ordinary cost margin still governs
	same := []Candidate{
		{Node: storage.Node{ID: 3, Name: "cur", BillingType: "按流量", TrafficPriceCents: 50, UpdatedAt: testNow}, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{ID: 4, Name: "win", BillingType: "按流量", TrafficPriceCents: 5, UpdatedAt: testNow}, Healthy: true, CostKnown: true, CostFresh: true},
	}
	if !shouldSwitch(PolicyCostFirst, "out", MissingCostDeprioritize, w, same, 0, 1) {
		t.Fatalf("same-tier 90pct saving must pass the ordinary margin")
	}
}

// TestSweepRespectsPriorityTier covers the end-to-end wiring: the sweep picks
// a higher-tier landing even when a lower-tier one is cheaper under the
// configured policy, and the tier survives the peak hysteresis (a fresh
// better tier switches even at peak).
func TestSweepRespectsPriorityTier(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.Local) // noon: peak window

	entry := seedNode(t, db, "pr-entry", "entry", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_entry"})
	})
	cheapLow := seedNode(t, db, "pr-cheap-low", "landing", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_landing"})
		n.BillingType, n.TrafficPriceCents, n.UpdatedAt = "按流量", int64(5), now
		n.SchedPriority = 2
	})
	dear := seedNode(t, db, "pr-dear-high", "landing", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_landing"})
		n.BillingType, n.TrafficPriceCents, n.UpdatedAt = "按流量", int64(80), now
		n.SchedPriority = 1
	})

	// fresh healthy tunnel verdicts toward both landings
	for _, l := range []storage.Node{cheapLow, dear} {
		lid := l.ID
		if err := db.Create(&storage.ProbeReport{
			NodeID: entry.ID, TargetKind: agentproto.ProbeTargetTunnel, TargetNodeID: &lid,
			Verdict: agentproto.ProbeVerdictHealthy, ProbedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	events, err := SweepAt(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != dear.Name {
		t.Fatalf("events = %+v, want the higher-tier %s to win", events, dear.Name)
	}

	// a second sweep with the entry now on the better tier keeps it (no churn)
	if events, err = SweepAt(db, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("second sweep churned: %+v", events)
	}
	var rows []storage.LandingAssignment
	if err := db.Where("released_at IS NULL AND entry_node_id = ?", entry.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].LandingNodeID != dear.ID {
		t.Fatalf("effective row = %+v", rows)
	}
}
