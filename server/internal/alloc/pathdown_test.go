package alloc

import (
	"io"
	"log"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestAlertSickPaths covers F-1823: a path whose landing leaves the
// schedulable pool or probes sick lands one path_down warning in the outbox;
// a healthy path stays quiet; the alert fires even with failover frozen
// (manual mode is exactly when the operator must hear about it).
func TestAlertSickPaths(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()

	entry := seedNode(t, db, "pd-entry", "entry", "out", nil)
	sickly := seedNode(t, db, "pd-sick", "landing", "out", nil)
	healthy := seedNode(t, db, "pd-ok", "landing", "out", nil)

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
	probe(healthy.ID, agentproto.ProbeVerdictHealthy)

	pSick := seedPath(t, db, "pd-path-sick", entry.ID, sickly.ID, "out")
	seedPath(t, db, "pd-path-ok", entry.ID, healthy.ID, "out")

	AlertSickPaths(db)

	var ev storage.Event
	if err := db.Where("kind = ?", herald.KindPathDown).First(&ev).Error; err != nil {
		t.Fatalf("path_down event missing: %v", err)
	}
	if ev.DedupKey != "path:"+strconv.Itoa(int(pSick.ID))+":path_down" {
		t.Fatalf("dedup key = %q", ev.DedupKey)
	}
	if ev.Severity != herald.SeverityWarning || ev.Target != herald.TargetAdmin {
		t.Fatalf("event shape: %+v", ev)
	}
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindPathDown).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("healthy path must stay quiet: %d events", n)
	}

	// Frozen failover still alerts: the sweep wires AlertSickPaths ahead of
	// the freeze check, so run one full round under failover=manual.
	if err := db.Create(&storage.Policy{Scope: "system", Constraints: `{"failover":"manual"}`, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := sweepOnce(db, log.New(io.Discard, "", 0), nil); err != nil {
		t.Fatalf("frozen sweep: %v", err)
	}
	var frozen storage.Event
	if err := db.Where("kind = ? AND id > ?", herald.KindPathDown, ev.ID).First(&frozen).Error; err != nil {
		t.Fatalf("frozen failover must still alert: %v", err)
	}
}

// TestAlertDegradedPaths covers F-1824: a path degraded by its entry going
// offline (landing still healthy) lands one path_degraded warning; a landing
// failure stays path_down's territory and never double-alerts.
func TestAlertDegradedPaths(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()

	entry := seedNode(t, db, "pdg-entry", "entry", "out", nil)
	landing := seedNode(t, db, "pdg-landing", "landing", "out", nil)

	if err := db.Create(&storage.ProbeReport{
		NodeID: entry.ID, TargetKind: agentproto.ProbeTargetTunnel, TargetNodeID: &landing.ID,
		Verdict: agentproto.ProbeVerdictHealthy, ProbedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	p := seedPath(t, db, "pdg-path", entry.ID, landing.ID, "out")

	// Healthy baseline: quiet.
	AlertDegradedPaths(db)
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindPathDegraded).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("healthy path must stay quiet: %d events", n)
	}

	// Entry goes offline: the path degrades without the landing failing.
	if err := db.Model(&storage.Node{}).Where("id = ?", entry.ID).
		Update("status", "offline").Error; err != nil {
		t.Fatal(err)
	}
	AlertDegradedPaths(db)
	var ev storage.Event
	if err := db.Where("kind = ?", herald.KindPathDegraded).First(&ev).Error; err != nil {
		t.Fatalf("path_degraded event missing: %v", err)
	}
	if ev.DedupKey != "path:"+strconv.Itoa(int(p.ID))+":path_degraded" {
		t.Fatalf("dedup key = %q", ev.DedupKey)
	}

	// A landing failure must not produce a second degraded alert.
	if err := db.Create(&storage.ProbeReport{
		NodeID: entry.ID, TargetKind: agentproto.ProbeTargetTunnel, TargetNodeID: &landing.ID,
		Verdict: agentproto.ProbeVerdictSick, ProbedAt: now.Add(time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindPathDegraded).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("landing failure must stay path_down's territory: %d degraded events", n)
	}
}
