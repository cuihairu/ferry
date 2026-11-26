package monitor

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestOverloadSweep covers the server-side overload warning (F-1067): at or
// above 80% utilization one active overload alert is created (a repeat round
// refreshes it, never stacking rows), dropping back under resolves it with a
// trail, and a node without samples never warns.
func TestOverloadSweep(t *testing.T) {
	db := openTestDB(t)
	over := storage.Node{Name: "ov-over", Token: "t-over", BwDownMbps: 100, Enabled: true}
	idle := storage.Node{Name: "ov-idle", Token: "t-idle", BwDownMbps: 100, Enabled: true}
	if err := db.Create(&over).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&idle).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	seed := func(node uint, rx, conns int64, at time.Time) {
		t.Helper()
		if err := db.Create(&storage.NodeTrafficLog{
			NodeID: node, Proc: "relay", RxBytes: rx, Conns: int(conns), RecordedAt: at,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	count := func() int64 {
		t.Helper()
		var n int64
		if err := db.Model(&storage.Alert{}).Where("node_id = ? AND kind = ?", over.ID, AlertKindOverload).
			Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}

	// 640MB over 60s -> 85.3 Mbps against the 100 Mbps plan -> 85%
	seed(over.ID, 0, 4, now.Add(-2*time.Minute))
	seed(over.ID, 640e6, 4, now.Add(-time.Minute))

	active, err := OverloadSweep(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if active != 1 || count() != 1 {
		t.Fatalf("overload must create one alert: active=%d rows=%d", active, count())
	}
	var row storage.Alert
	if err := db.Where("node_id = ? AND kind = ?", over.ID, AlertKindOverload).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.State != "active" || row.Severity != "warning" {
		t.Fatalf("alert = %+v", row)
	}

	// a repeat round refreshes the same row instead of stacking another
	if active, err = OverloadSweep(db, now); err != nil {
		t.Fatal(err)
	}
	if active != 1 || count() != 1 {
		t.Fatalf("repeat round must not stack: active=%d rows=%d", active, count())
	}

	// traffic falls back: 100MB over 60s -> 13.3 Mbps -> 13%, the alert resolves
	seed(over.ID, 740e6, 4, now)
	if active, err = OverloadSweep(db, now); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("recovered node must not stay active: %d", active)
	}
	var resolved storage.Alert
	if err := db.Where("node_id = ? AND kind = ?", over.ID, AlertKindOverload).First(&resolved).Error; err != nil {
		t.Fatal(err)
	}
	if resolved.State != "resolved" || resolved.ResolvedAt == nil {
		t.Fatalf("resolved alert = %+v", resolved)
	}

	// the idle node never had samples, so it never warned
	var idleN int64
	if err := db.Model(&storage.Alert{}).Where("node_id = ?", idle.ID).Count(&idleN).Error; err != nil {
		t.Fatal(err)
	}
	if idleN != 0 {
		t.Fatalf("sampleless node must not warn: %d", idleN)
	}
}
