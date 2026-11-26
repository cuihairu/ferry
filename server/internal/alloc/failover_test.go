package alloc

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// seedPath is a direct entry->landing path at version 1.
func seedPath(t *testing.T, db *gorm.DB, name string, entry, landing uint, direction string) storage.Path {
	t.Helper()
	p := storage.Path{Name: name, EntryNodeID: entry, LandingNodeID: landing,
		Direction: direction, Enabled: true, Version: 1}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRebindSickPaths covers the failover executor (F-1031): a path whose
// landing reports a fresh sick verdict rebinds to the policy's best healthy
// alternative with a version bump; a path with no healthy alternative stays
// put; a healthy landing is a no-op; failover=manual freezes the pass.
func TestRebindSickPaths(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()

	entry := seedNode(t, db, "fo-entry", "entry", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_entry"})
	})
	sickly := seedNode(t, db, "fo-sick", "landing", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_landing"})
		n.BillingType, n.TrafficPriceCents = "按流量", int64(5)
	})
	alt := seedNode(t, db, "fo-alt", "landing", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_landing"})
		n.BillingType, n.TrafficPriceCents = "按流量", int64(50)
	})
	backup := seedNode(t, db, "fo-backup", "landing", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_landing"})
		n.BillingType, n.TrafficPriceCents = "按流量", int64(90)
	})

	probe := func(target uint, verdict string) {
		t.Helper()
		tid := target
		if err := db.Create(&storage.ProbeReport{
			NodeID: entry.ID, TargetKind: agentproto.ProbeTargetTunnel, TargetNodeID: &tid,
			Verdict: verdict, ProbedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	probe(sickly.ID, agentproto.ProbeVerdictSick)
	probe(alt.ID, agentproto.ProbeVerdictHealthy)
	probe(backup.ID, agentproto.ProbeVerdictHealthy)

	pSick := seedPath(t, db, "fo-path-sick", entry.ID, sickly.ID, "out")
	pHealthy := seedPath(t, db, "fo-path-ok", entry.ID, alt.ID, "out")

	// frozen failover skips the pass entirely
	if err := db.Create(&storage.Policy{Scope: "system", Constraints: `{"failover":"manual"}`, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	events, err := RebindSickPaths(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("frozen failover rebinds = %+v", events)
	}
	if err := db.Where("1=1").Delete(&storage.Policy{}).Error; err != nil {
		t.Fatal(err)
	}

	events, err = RebindSickPaths(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want exactly the sick path", events)
	}
	ev := events[0]
	if ev.PathID != pSick.ID || ev.FromID != sickly.ID || ev.ToID != alt.ID {
		t.Fatalf("event = %+v", ev)
	}
	if ev.Reason == "" || ev.SwitchEvent().ToName != alt.Name {
		t.Fatalf("event rendering = %+v", ev)
	}

	// the sick path rebinds with a version bump; the healthy one is untouched
	// (fresh dest per lookup: a reused struct keeps its primary key, and GORM
	// folds that into the next query as a hidden condition)
	var reloaded storage.Path
	if err := db.First(&reloaded, pSick.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.LandingNodeID != alt.ID || reloaded.Version != pSick.Version+1 {
		t.Fatalf("rebound path = %+v", reloaded)
	}
	var intact storage.Path
	if err := db.First(&intact, pHealthy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if intact.LandingNodeID != alt.ID || intact.Version != 1 {
		t.Fatalf("healthy path must stay: %+v", intact)
	}

	// a second pass is a no-op: the rebound landing is healthy now
	if events, err = RebindSickPaths(db, now); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("second pass events = %+v", events)
	}

	// no healthy alternative: the path stays put
	probe(alt.ID, agentproto.ProbeVerdictSick)
	probe(backup.ID, agentproto.ProbeVerdictSick)
	pBoth := seedPath(t, db, "fo-path-trapped", entry.ID, alt.ID, "out")
	if events, err = RebindSickPaths(db, now); err != nil {
		t.Fatal(err)
	}
	// backup now holds the only healthy verdict-free... it is sick too, so the
	// trapped path has nowhere to go; nothing rebinds this round.
	for _, ev := range events {
		if ev.PathID == pBoth.ID {
			t.Fatalf("trapped path must stay put: %+v", ev)
		}
	}
	var trapped storage.Path
	if err := db.First(&trapped, pBoth.ID).Error; err != nil {
		t.Fatal(err)
	}
	if trapped.LandingNodeID != alt.ID || trapped.Version != 1 {
		t.Fatalf("trapped path changed: %+v", trapped)
	}
}

// TestRebindLandingLeftPool covers the pool-loss branch: a path whose landing
// row is disabled (no longer a schedulable pool member) rebinds even without
// any probe verdict at all.
func TestRebindLandingLeftPool(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()

	entry := seedNode(t, db, "fp-entry", "entry", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_entry"})
	})
	gone := seedNode(t, db, "fp-gone", "landing", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_landing"})
	})
	alive := seedNode(t, db, "fp-alive", "landing", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_landing"})
	})
	p := seedPath(t, db, "fp-path", entry.ID, gone.ID, "out")

	// disable the landing: it drops out of the schedulable pool
	if err := db.Model(&storage.Node{}).Where("id = ?", gone.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}

	events, err := RebindSickPaths(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].PathID != p.ID || events[0].ToID != alive.ID {
		t.Fatalf("events = %+v, want the orphaned path rebound to %s", events, alive.Name)
	}
	var reloaded storage.Path
	if err := db.First(&reloaded, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.LandingNodeID != alive.ID || reloaded.Version != 2 {
		t.Fatalf("rebound path = %+v", reloaded)
	}
}
