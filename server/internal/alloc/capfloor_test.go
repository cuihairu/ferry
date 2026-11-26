package alloc

import (
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCapacityFloorDemotion covers the capacity demotion (F-1069): with the
// floor at 100 Mbps, a small pipe loses to an adequate one under cost_first
// even when it is cheaper, the demotion stays inside the operator's tier
// (a better tier outranks the floor), and unknown bandwidth never demotes.
func TestCapacityFloorDemotion(t *testing.T) {
	big := Candidate{Node: storage.Node{Name: "cf-big", BwDownMbps: 500, BillingType: "按流量", TrafficPriceCents: 50}, Healthy: true, CostKnown: true, CostFresh: true}
	small := Candidate{Node: storage.Node{Name: "cf-small", BwDownMbps: 30, BillingType: "按流量", TrafficPriceCents: 5}, Healthy: true, CostKnown: true, CostFresh: true}
	unknown := Candidate{Node: storage.Node{Name: "cf-unknown", BillingType: "按流量", TrafficPriceCents: 5}, Healthy: true}
	w := ScoreWeights{CapacityFloor: 100}

	// floor on: the adequate pipe wins over the cheaper small one, and the
	// unknown node is not demoted for its unknown bandwidth -- on the same
	// price it outranks the demoted small pipe (and under cost_first with
	// the floor off the small pipe would win that pair by name)
	less := LessFor(PolicyCostFirst, "out", MissingCostDeprioritize, w, []Candidate{big, small, unknown})
	if !less(0, 1) || less(1, 0) {
		t.Fatal("small pipe must demote below the adequate one")
	}
	if !less(2, 1) {
		t.Fatal("unknown bandwidth must not demote")
	}

	// floor off: cost_first picks the cheap small pipe again
	knownSmall := Candidate{Node: small.Node, Healthy: true, CostKnown: true, CostFresh: true}
	off := LessFor(PolicyCostFirst, "out", MissingCostDeprioritize, ScoreWeights{}, []Candidate{big, knownSmall})
	if !off(1, 0) || off(0, 1) {
		t.Fatal("floor off must restore the policy order")
	}

	// the operator's tier outranks the floor: a small pipe in a better tier
	// still wins over an adequate pipe in a worse tier
	tiered := []Candidate{
		{Node: storage.Node{Name: "cf-t0-small", BwDownMbps: 30, BillingType: "按流量", TrafficPriceCents: 50, SchedPriority: 0}, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{Name: "cf-t3-big", BwDownMbps: 500, BillingType: "按流量", TrafficPriceCents: 5, SchedPriority: 3}, Healthy: true, CostKnown: true, CostFresh: true},
	}
	less = LessFor(PolicyCostFirst, "out", MissingCostDeprioritize, w, tiered)
	if !less(0, 1) || less(1, 0) {
		t.Fatal("tier must outrank the capacity floor")
	}
}

// TestSweepCapacityFloor covers the demotion at the sweep level (F-1069):
// the policy persists, the sweep hands the entry to the adequate landing,
// and a settled round stays quiet.
func TestSweepCapacityFloor(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "sf-entry", "entry", "out", nil)
	seedNode(t, db, "sf-small", "landing", "out", func(n *storage.Node) {
		n.BwDownMbps = 30
		n.BillingType, n.TrafficPriceCents = "按流量", 5
	})
	big := seedNode(t, db, "sf-big", "landing", "out", func(n *storage.Node) {
		n.BwDownMbps = 500
		n.BillingType, n.TrafficPriceCents = "按流量", 50
	})

	setting := DefaultPolicySetting() // out = cost_first
	setting.MinCapacityFloor = 100
	if err := SavePolicy(db, setting); err != nil {
		t.Fatal(err)
	}
	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != big.Name {
		t.Fatalf("floor must demote the small pipe: %+v", events)
	}
	if events, err = Sweep(db); err != nil || len(events) != 0 {
		t.Fatalf("settled round must be quiet: %+v err=%v", events, err)
	}
}

// TestValidateSettingCapacityFloor covers the floor check (F-1069): negative
// floors are rejected, zero (disabled) and positive floors are accepted.
func TestValidateSettingCapacityFloor(t *testing.T) {
	ok := DefaultPolicySetting()
	ok.MinCapacityFloor = 100
	if _, err := ValidateSetting(ok); err != nil {
		t.Fatalf("positive floor rejected: %v", err)
	}
	bad := DefaultPolicySetting()
	bad.MinCapacityFloor = -1
	if _, err := ValidateSetting(bad); err == nil {
		t.Fatal("negative floor accepted")
	}
}
