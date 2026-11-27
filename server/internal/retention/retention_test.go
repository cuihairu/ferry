package retention

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTestDB opens an in-memory DB and migrates every table (isolated per test,
// same recipe as the other packages' harnesses).
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:retention-%s-%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestSweepPrunes covers F-1910: out-of-window probe reports and node traffic
// samples are deleted, in-window rows and the user traffic ledger survive,
// and a days<=0 window keeps everything (permanent retention).
func TestSweepPrunes(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	old := now.AddDate(0, 0, -40)

	node := storage.Node{Name: "rt-1", Token: "rt-t1"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	user := storage.User{Username: "rt-u", SubToken: "rt-s1", QuotaBytes: 1 << 30, Enabled: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	uid := user.ID
	rows := []any{
		&storage.ProbeReport{NodeID: node.ID, TargetKind: "tunnel", TargetNodeID: &node.ID, Verdict: "healthy", ProbedAt: old},
		&storage.ProbeReport{NodeID: node.ID, TargetKind: "tunnel", TargetNodeID: &node.ID, Verdict: "healthy", ProbedAt: now},
		&storage.NodeTrafficLog{NodeID: node.ID, Proc: "xray", RxBytes: 1, RecordedAt: old},
		&storage.NodeTrafficLog{NodeID: node.ID, Proc: "xray", RxBytes: 1, RecordedAt: now},
		// The user ledger is deliberately never pruned (accounting source).
		&storage.TrafficLog{UserID: &uid, RxBytes: 1, TxBytes: 1, RecordedAt: old},
	}
	for _, r := range rows {
		if err := db.Create(r).Error; err != nil {
			t.Fatal(err)
		}
	}

	pn, tn, err := Sweep(db, 30, 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if pn != 1 || tn != 1 {
		t.Fatalf("pruned probe=%d traffic=%d, want 1/1", pn, tn)
	}
	var probe, nodeTraffic, userTraffic int64
	db.Model(&storage.ProbeReport{}).Count(&probe)
	db.Model(&storage.NodeTrafficLog{}).Count(&nodeTraffic)
	db.Model(&storage.TrafficLog{}).Count(&userTraffic)
	if probe != 1 || nodeTraffic != 1 {
		t.Fatalf("in-window rows must survive: probe=%d traffic=%d", probe, nodeTraffic)
	}
	if userTraffic != 1 {
		t.Fatalf("user traffic ledger must never be pruned: %d rows", userTraffic)
	}

	// days<=0 is permanent retention for that table.
	pn, tn, err = Sweep(db, 0, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if pn != 0 || tn != 0 {
		t.Fatalf("zero retention must keep everything: probe=%d traffic=%d", pn, tn)
	}
}

// TestLoopSweepsAndStops covers the loop shell: a shortened tick prunes
// out-of-window rows, ctx cancellation returns.
func TestLoopSweepsAndStops(t *testing.T) {
	old := sweepInterval
	sweepInterval = 5 * time.Millisecond
	defer func() { sweepInterval = old }()

	db := newTestDB(t)
	now := time.Now()
	node := storage.Node{Name: "rt-2", Token: "rt-t2"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.ProbeReport{NodeID: node.ID, TargetKind: "tunnel",
		TargetNodeID: &node.ID, Verdict: "healthy", ProbedAt: now.AddDate(0, 0, -40)}).Error; err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 30, 62, nil)
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var n int64
		db.Model(&storage.ProbeReport{}).Count(&n)
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("loop never pruned: %d rows left", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not stop on ctx cancel")
	}
}
