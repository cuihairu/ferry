package alloc

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestMissingCostDeprioritized covers F-0605/F-0610 at the comparator level: an
// unknown or stale metered price is never treated as free — it sorts behind
// every priced candidate under the default rule, and treat_zero restores the
// legacy free-for-unpriced behavior.
func TestMissingCostDeprioritized(t *testing.T) {
	priced := Candidate{Node: storage.Node{Name: "priced", BillingType: "按流量", TrafficPriceCents: 100}, Healthy: true, CostKnown: true, CostFresh: true}
	unpriced := Candidate{Node: storage.Node{Name: "unpriced", BillingType: "按流量", TrafficPriceCents: 0}, Healthy: true}
	fixed := Candidate{Node: storage.Node{Name: "fixed", BillingType: "包月"}, Healthy: true}

	// cost_first with a priced set: the fixed landing (marginal 0) wins.
	cands := []Candidate{priced, unpriced, fixed}
	idx, ok := Pick(PolicyCostFirst, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "fixed" {
		t.Fatalf("fixed should win with priced present: idx=%d ok=%v", idx, ok)
	}

	// The unpriced metered node is missing under the default rule: it loses to
	// the priced one despite reading as "0".
	cands = []Candidate{priced, unpriced}
	idx, ok = Pick(PolicyCostFirst, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "priced" {
		t.Fatalf("missing price must lose to a priced one: idx=%d ok=%v", idx, ok)
	}

	// A stale price degrades the same way (F-0604 freshness).
	stale := unpriced
	stale.CostKnown, stale.CostFresh = true, false
	stale.Node.Name = "stale"
	stale.Node.TrafficPriceCents = 1
	cands = []Candidate{priced, stale}
	idx, ok = Pick(PolicyCostFirst, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "priced" {
		t.Fatalf("stale price must lose to a fresh one: idx=%d ok=%v", idx, ok)
	}

	// treat_zero restores the legacy behavior: the unpriced metered node wins as free.
	cands = []Candidate{priced, unpriced}
	idx, ok = Pick(PolicyCostFirst, "out", MissingCostTreatZero, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "unpriced" {
		t.Fatalf("treat_zero must keep legacy free behavior: idx=%d ok=%v", idx, ok)
	}
}

// TestSweepMissingCostDeprioritized covers F-0604/F-0605 at sweep level:
// SweepAt fills candidate freshness from the node price fields, so a stale
// cheap price loses to a fresh higher one under the default rule, and
// treat_zero flips it back.
func TestSweepMissingCostDeprioritized(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "entry", "entry", "out", nil)
	cheap := seedNode(t, db, "land-cheap", "landing", "out", func(n *storage.Node) {
		n.BillingType = "按流量"
		n.TrafficPriceCents = 10
	})
	pricey := seedNode(t, db, "land-pricey", "landing", "out", func(n *storage.Node) {
		n.BillingType = "按流量"
		n.TrafficPriceCents = 100
	})
	// Pin cheap's price row 9 days back: beyond PriceFreshWindow it is stale.
	if err := db.Model(&storage.Node{}).Where("id = ?", cheap.ID).
		UpdateColumn("updated_at", time.Now().Add(-9*24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}

	// Default rule (deprioritize): the stale cheap price must not win as free;
	// the fresh pricey landing is picked instead.
	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != pricey.Name {
		t.Fatalf("stale cheap must lose to fresh pricey: %+v", events)
	}

	// treat_zero restores the legacy free behavior: the stale node wins.
	setting := DefaultPolicySetting()
	setting.MissingCost = MissingCostTreatZero
	if err := SavePolicy(db, setting); err != nil {
		t.Fatal(err)
	}
	if _, err := Sweep(db); err != nil {
		t.Fatal(err)
	}
	var row storage.LandingAssignment
	if err := db.Where("released_at IS NULL AND entry_node_id IS NOT NULL").
		Order("id DESC").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.LandingNodeID != cheap.ID {
		t.Fatalf("treat_zero must let the stale free-looking node win: landing %d want %d", row.LandingNodeID, cheap.ID)
	}
}

// TestCostCeilingExcludes covers F-0606/F-0610: SweepAt excludes metered
// candidates whose known fresh price exceeds the configured ceiling and trails
// the exclusion (deduped within an hour), while unknown or stale prices are
// never excluded.
func TestCostCeilingExcludes(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "entry", "entry", "out", nil)
	cheap := seedNode(t, db, "land-cheap", "landing", "out", func(n *storage.Node) {
		n.BillingType = "按流量"
		n.TrafficPriceCents = 10
	})
	pricey := seedNode(t, db, "land-pricey", "landing", "out", func(n *storage.Node) {
		n.BillingType = "按流量"
		n.TrafficPriceCents = 500
	})
	mystery := seedNode(t, db, "land-mystery", "landing", "out", func(n *storage.Node) {
		n.BillingType = "按流量"
		n.TrafficPriceCents = 0 // metered but unpriced: never excluded (F-0610)
	})

	// Ceiling off (default 0): the cheapest priced landing wins and nothing is trailed.
	if _, err := Sweep(db); err != nil {
		t.Fatal(err)
	}
	var rows int64
	if err := db.Model(&storage.AllocExclusion{}).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("no ceiling set: exclusion rows = %d, want 0", rows)
	}

	// Ceiling 100: pricey exceeds it, is excluded this round and trailed once.
	setting := DefaultPolicySetting()
	setting.MaxCostPerGB = 100
	if err := SavePolicy(db, setting); err != nil {
		t.Fatal(err)
	}
	if _, err := Sweep(db); err != nil {
		t.Fatal(err)
	}
	var ex []storage.AllocExclusion
	if err := db.Find(&ex).Error; err != nil {
		t.Fatal(err)
	}
	if len(ex) != 1 || ex[0].LandingNodeID != pricey.ID {
		t.Fatalf("only the over-ceiling landing must be trailed: %+v", ex)
	}
	if ex[0].EntryNodeID == 0 || ex[0].Policy != PolicyCostFirst || ex[0].Direction != agentproto.DirectionOut {
		t.Fatalf("exclusion trail fields: %+v", ex[0])
	}
	// The cheap landing (10 ≤ 100) keeps the assignment.
	var row storage.LandingAssignment
	if err := db.Where("released_at IS NULL AND entry_node_id IS NOT NULL").
		Order("id DESC").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.LandingNodeID != cheap.ID {
		t.Fatalf("cheap landing must keep the assignment: landing %d", row.LandingNodeID)
	}

	// Repeated sweeps dedupe the trail: still one row for pricey.
	for i := 0; i < 2; i++ {
		if _, err := Sweep(db); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&storage.AllocExclusion{}).Where("landing_node_id = ?", pricey.ID).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("exclusion trail must dedupe: rows = %d, want 1", rows)
	}

	// The unpriced metered landing is never trailed (F-0610), even though its
	// unknown price could turn out to burst the ceiling.
	if err := db.Model(&storage.AllocExclusion{}).Where("landing_node_id = ?", mystery.ID).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("unknown price must not be excluded: rows = %d, want 0", rows)
	}

	// The mystery landing becomes priced fresh below the ceiling: the stale
	// cheap price now degrades to missing and loses to the fresh low price.
	if err := db.Model(&storage.Node{}).Where("id = ?", mystery.ID).
		Update("traffic_price_cents", 5).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.Node{}).Where("id = ?", cheap.ID).
		UpdateColumn("updated_at", time.Now().Add(-9*24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != mystery.Name {
		t.Fatalf("stale price must lose to the fresh low price: %+v", events)
	}
}

// TestPolicyCostGuardValidation covers the guard fields on the policy setting:
// validation rejects negative ceilings and unknown degradation rules, and
// settings persisted before the fields existed fall back to the defaults.
func TestPolicyCostGuardValidation(t *testing.T) {
	db := newTestDB(t)
	bad := DefaultPolicySetting()
	bad.MaxCostPerGB = -1
	if err := SavePolicy(db, bad); err == nil {
		t.Fatal("negative ceiling must be rejected")
	}
	bad = DefaultPolicySetting()
	bad.MissingCost = "free"
	if err := SavePolicy(db, bad); err == nil {
		t.Fatal("unknown missing-cost rule must be rejected")
	}
	ok := DefaultPolicySetting()
	ok.MaxCostPerGB = 42
	ok.MissingCost = MissingCostTreatZero
	if err := SavePolicy(db, ok); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPolicy(db)
	if err != nil || got.MaxCostPerGB != 42 || got.MissingCost != MissingCostTreatZero {
		t.Fatalf("round trip: %+v err=%v", got, err)
	}

	// A legacy setting JSON without the guard fields normalizes to the defaults.
	legacy := `{"out":"cost_first","in":"perf_first","rebalance_start":1,"rebalance_end":7}`
	if err := storage.SetSetting(db, settingKey, legacy); err != nil {
		t.Fatal(err)
	}
	got, err = LoadPolicy(db)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxCostPerGB != 0 || got.MissingCost != DefaultMissingCost {
		t.Fatalf("legacy setting must default the guards: %+v", got)
	}
}
