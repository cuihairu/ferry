package save

import (
	"context"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// seedTraffic inserts traffic rows directly (one row per report timestamp).
func seedTraffic(t *testing.T, db *gorm.DB, rows ...storage.NodeTrafficLog) {
	t.Helper()
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}
}

func statRows(t *testing.T, db *gorm.DB) []storage.SaveStat {
	t.Helper()
	var rows []storage.SaveStat
	if err := db.Order("day ASC, node_id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query save stats: %v", err)
	}
	return rows
}

// TestSweepAggregatesByDay covers the main path: traffic rows are aggregated by (node, UTC day) into
// save_stats, a second sweep at the same watermark does not double count, and crossing days splits into separate rows.
func TestSweepAggregatesByDay(t *testing.T) {
	db := newTestDB(t)
	day1 := time.Date(2026, 10, 7, 23, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC)
	seedTraffic(t, db,
		storage.NodeTrafficLog{NodeID: 1, Proc: "xray", DirectBytes: 100, BlockedBytes: 10, RecordedAt: day1},
		storage.NodeTrafficLog{NodeID: 1, Proc: "xray", DirectBytes: 50, BlockedBytes: 5, RecordedAt: day1},
		storage.NodeTrafficLog{NodeID: 2, Proc: "xray", DirectBytes: 7, RecordedAt: day1},
		storage.NodeTrafficLog{NodeID: 1, Proc: "xray", DirectBytes: 200, BlockedBytes: 20, RecordedAt: day2},
	)

	if n, err := Sweep(db); err != nil || n != 4 {
		t.Fatalf("sweep = %d, %v; want 4 rows", n, err)
	}
	rows := statRows(t, db)
	if len(rows) != 3 {
		t.Fatalf("save stats = %d rows, want 3 (2 节点×同日 + 1 跨日): %+v", len(rows), rows)
	}
	// node 1 day 1: the two reports are summed; node 1 day 2 is its own row
	if rows[0].NodeID != 1 || rows[0].Day != "2026-10-07" || rows[0].DirectBytes != 150 || rows[0].BlockedBytes != 15 {
		t.Fatalf("node1 day1 mismatch: %+v", rows[0])
	}
	if rows[1].NodeID != 2 || rows[1].Day != "2026-10-07" || rows[1].DirectBytes != 7 || rows[1].BlockedBytes != 0 {
		t.Fatalf("node2 day1 mismatch: %+v", rows[1])
	}
	if rows[2].NodeID != 1 || rows[2].Day != "2026-10-08" || rows[2].DirectBytes != 200 || rows[2].BlockedBytes != 20 {
		t.Fatalf("node1 day2 mismatch: %+v", rows[2])
	}

	// Second sweep: the watermark has advanced, so nothing is counted twice
	if n, err := Sweep(db); err != nil || n != 0 {
		t.Fatalf("second sweep = %d, %v; want 0", n, err)
	}
	rows = statRows(t, db)
	if rows[0].DirectBytes != 150 {
		t.Fatalf("second sweep must not double count: %+v", rows[0])
	}

	// Incremental accounting: new rows after the watermark only accumulate the new delta
	seedTraffic(t, db,
		storage.NodeTrafficLog{NodeID: 1, Proc: "xray", DirectBytes: 25, RecordedAt: day2},
	)
	if n, err := Sweep(db); err != nil || n != 1 {
		t.Fatalf("third sweep = %d, %v; want 1", n, err)
	}
	rows = statRows(t, db)
	if rows[2].DirectBytes != 225 {
		t.Fatalf("incremental aggregate = %d, want 225", rows[2].DirectBytes)
	}
}

// TestSweepRebuildsWithoutWatermark covers a lost watermark: save_stats is cleared and rebuilt in full,
// without stacking on top of the historical rows.
func TestSweepRebuildsWithoutWatermark(t *testing.T) {
	db := newTestDB(t)
	day := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	seedTraffic(t, db,
		storage.NodeTrafficLog{NodeID: 1, Proc: "xray", DirectBytes: 100, RecordedAt: day},
	)
	if _, err := Sweep(db); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// Simulate a lost watermark while save_stats still has historical rows
	if err := db.Where("key=?", settingWatermark).Delete(&storage.Setting{}).Error; err != nil {
		t.Fatalf("drop watermark: %v", err)
	}
	if _, err := Sweep(db); err != nil {
		t.Fatalf("rebuild sweep: %v", err)
	}
	rows := statRows(t, db)
	if len(rows) != 1 || rows[0].DirectBytes != 100 {
		t.Fatalf("rebuild must replace, not accumulate: %+v", rows)
	}
}

// TestUserSavings covers the SAVE-8 conversion: node savings are apportioned by each user's share of the month's billed
// traffic; a user with no billing gets a share of 0 and is not counted, a node with no billing for the month is not apportioned, and data before the month window is excluded.
func TestUserSavings(t *testing.T) {
	db := newTestDB(t)
	users := []storage.User{
		{ID: 1, Username: "u1", SubToken: "tk1"},
		{ID: 2, Username: "u2", SubToken: "tk2"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	nodes := []storage.Node{
		{ID: 1, Name: "n1", Token: "tok-n1"},
		{ID: 2, Name: "n2", Token: "tok-n2"},
		{ID: 3, Name: "n3", Token: "tok-n3"},
	}
	if err := db.Create(&nodes).Error; err != nil {
		t.Fatalf("seed nodes: %v", err)
	}
	stats := []storage.SaveStat{
		{NodeID: 1, Day: "2026-10-03", DirectBytes: 1000, CacheHitBytes: 200, BlockedBytes: 100},
		{NodeID: 2, Day: "2026-10-04", DirectBytes: 500},
		{NodeID: 3, Day: "2026-10-05", DirectBytes: 777},
		{NodeID: 1, Day: "2026-09-28", DirectBytes: 9999}, // before the month window, not counted
	}
	if err := db.Create(&stats).Error; err != nil {
		t.Fatalf("seed save stats: %v", err)
	}
	n1, n2 := uint(1), uint(2)
	u1, u2 := uint(1), uint(2)
	monthStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	priorMonth := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	logs := []storage.TrafficLog{
		{UserID: &u1, NodeID: &n1, RxBytes: 300, TxBytes: 100, RecordedAt: monthStart}, // node 1: own 400
		{UserID: &u2, NodeID: &n1, RxBytes: 600, TxBytes: 0, RecordedAt: monthStart},   // node 1: someone else's 600
		{UserID: &u2, NodeID: &n2, RxBytes: 600, TxBytes: 0, RecordedAt: monthStart},   // node 2: only someone else's billing
		{UserID: &u1, NodeID: &n1, RxBytes: 5000, TxBytes: 0, RecordedAt: priorMonth},  // before the month window, not counted
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatalf("seed traffic logs: %v", err)
	}

	direct, cacheHit, blocked, err := UserSavings(db, 1, "2026-10-01")
	if err != nil {
		t.Fatalf("UserSavings: %v", err)
	}
	// node 1: the share is 400/1000=0.4 -> 1000/200/100 are each multiplied by 0.4;
	// node 2's someone-else share is 0 so it is not counted; node 3 has no billing so nothing is apportioned; September data is excluded.
	if direct != 400 || cacheHit != 80 || blocked != 40 {
		t.Fatalf("UserSavings = %d, %d, %d; want 400, 80, 40", direct, cacheHit, blocked)
	}
}

// TestLoopSweepsAndStopsOnCancel covers Loop start/stop: one round runs right at startup (timer 0),
// it exits after ctx is cancelled, and a nil logger falls back to the default logger.
func TestLoopSweepsAndStopsOnCancel(t *testing.T) {
	db := newTestDB(t)
	seedTraffic(t, db,
		storage.NodeTrafficLog{NodeID: 1, Proc: "xray", DirectBytes: 100, RecordedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 20*time.Millisecond, nil) // the nil-logger fallback branch
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var rows []storage.SaveStat
		if err := db.Find(&rows).Error; err != nil {
			t.Fatalf("query save stats: %v", err)
		}
		if len(rows) == 1 && rows[0].DirectBytes == 100 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("loop first sweep never landed: %+v", rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after cancel")
	}
}
