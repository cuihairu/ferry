package alloc

import (
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestIsDirectTo covers the structural direct/relay split (F-1051): a
// landing sharing the entry's non-empty region is direct, everything else
// (other region, unknown entry region) crosses a relay segment.
func TestIsDirectTo(t *testing.T) {
	entry := storage.Node{Region: "上海"}
	if !isDirectTo(entry, storage.Node{Region: "上海"}) {
		t.Fatal("same region must be direct")
	}
	if isDirectTo(entry, storage.Node{Region: "hk"}) {
		t.Fatal("other region must be relay")
	}
	if isDirectTo(storage.Node{}, storage.Node{Region: "上海"}) {
		t.Fatal("unknown entry region must not claim direct")
	}
}

// TestSweepDirectFirst covers the default rule (F-1051) and the comparison
// trail (F-1054): a domestic entry prefers the same-region landing even when
// the policy order (cost_first) favors the cheap cross-region relay, and the
// relay candidate's exclusion reason carries the quality scores the rule
// decided from.
func TestSweepDirectFirst(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "df-entry", "entry", "out", func(n *storage.Node) { n.Region = "上海" })
	direct := seedNode(t, db, "df-direct", "landing", "out", func(n *storage.Node) {
		n.Region = "上海"
		n.BillingType, n.TrafficPriceCents = "按流量", 90
	})
	relay := seedNode(t, db, "df-relay", "landing", "out", func(n *storage.Node) {
		n.Region = "hk"
		n.BillingType, n.TrafficPriceCents = "按流量", 5
	})

	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EntryName != entry.Name || events[0].ToName != direct.Name {
		t.Fatalf("direct must win by default: %+v", events)
	}
	// F-1054: the relay landing's exclusion trails the comparison basis.
	var ex storage.AllocExclusion
	if err := db.Where("entry_node_id = ? AND landing_node_id = ?", entry.ID, relay.ID).
		Order("id DESC").First(&ex).Error; err != nil {
		t.Fatalf("relay exclusion missing: %v", err)
	}
	if !strings.Contains(ex.Reason, "直连优先") {
		t.Fatalf("exclusion reason = %s", ex.Reason)
	}
}

// TestSweepRelayProvenBetter covers the measurement exception (F-1052): when
// fresh probes prove the relay clearly better than the direct landing, the
// domestic entry hands its traffic to the relay and the direct candidate's
// exclusion carries the comparison that allowed it.
func TestSweepRelayProvenBetter(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "rp-entry", "entry", "out", func(n *storage.Node) { n.Region = "上海" })
	direct := seedNode(t, db, "rp-direct", "landing", "out", func(n *storage.Node) { n.Region = "上海" })
	relay := seedNode(t, db, "rp-relay", "landing", "out", func(n *storage.Node) { n.Region = "hk" })

	seed := func(target uint, rtt, loss int) {
		t.Helper()
		tid := target
		if err := db.Create(&storage.ProbeReport{
			NodeID: entry.ID, TargetKind: agentproto.ProbeTargetTunnel, TargetNodeID: &tid,
			Verdict: agentproto.ProbeVerdictHealthy, RttMs: rtt, LossPct: loss, ProbedAt: time.Now(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed(relay.ID, 10, 0)    // clean relay line
	seed(direct.ID, 500, 50) // congested direct line

	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != relay.Name {
		t.Fatalf("proven relay must win: %+v", events)
	}
	if !strings.Contains(events[0].Reason, "中继更优") {
		t.Fatalf("switch reason = %s", events[0].Reason)
	}
	var ex storage.AllocExclusion
	if err := db.Where("entry_node_id = ? AND landing_node_id = ?", entry.ID, direct.ID).
		Order("id DESC").First(&ex).Error; err != nil {
		t.Fatalf("direct exclusion missing: %v", err)
	}
	if !strings.Contains(ex.Reason, "测量证明中继更优") {
		t.Fatalf("exclusion reason = %s", ex.Reason)
	}
}

// TestSweepDirectFallback covers the unavailability fallback (F-1053): with
// the same-region landing sick, the rule steps aside and the policy picks
// the relay; no direct-first suppression is trailed.
func TestSweepDirectFallback(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "fb-entry", "entry", "out", func(n *storage.Node) { n.Region = "上海" })
	direct := seedNode(t, db, "fb-direct", "landing", "out", func(n *storage.Node) { n.Region = "上海" })
	relay := seedNode(t, db, "fb-relay", "landing", "out", func(n *storage.Node) { n.Region = "hk" })

	seedProbe(t, db, direct.ID, agentproto.ProbeVerdictSick)

	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != relay.Name {
		t.Fatalf("policy must pick the relay when direct is sick: %+v", events)
	}
	var n int64
	if err := db.Model(&storage.AllocExclusion{}).
		Where("entry_node_id = ?", entry.ID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("fallback must not trail direct-first exclusions: %d", n)
	}
}

// TestDirectFirstToggle covers the policy switch: direct_first=false restores
// the pure policy order (cheap relay wins over pricey direct).
func TestDirectFirstToggle(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "tg-entry", "entry", "out", func(n *storage.Node) { n.Region = "上海" })
	seedNode(t, db, "tg-direct", "landing", "out", func(n *storage.Node) {
		n.Region = "上海"
		n.BillingType, n.TrafficPriceCents = "按流量", 90
	})
	relay := seedNode(t, db, "tg-relay", "landing", "out", func(n *storage.Node) {
		n.Region = "hk"
		n.BillingType, n.TrafficPriceCents = "按流量", 5
	})

	off := false
	setting := DefaultPolicySetting()
	setting.DirectFirst = &off
	if err := SavePolicy(db, setting); err != nil {
		t.Fatal(err)
	}
	if on := directFirstOn(setting); on {
		t.Fatal("explicit false must disable the rule")
	}
	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EntryName != entry.Name || events[0].ToName != relay.Name {
		t.Fatalf("disabled rule must follow cost_first: %+v", events)
	}
	// rule off: no comparison trail either
	var n int64
	if err := db.Model(&storage.AllocExclusion{}).
		Where("entry_node_id = ?", entry.ID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("disabled rule must not trail exclusions: %d", n)
	}
}
