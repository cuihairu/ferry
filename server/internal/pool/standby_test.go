package pool

import (
	"errors"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestStandbyLifecycle covers the standby list (BR-3): marking an in-pool entry standby (out of
// subscriptions but registered and waiting), no re-marking from suspended/standby, manual return
// to the pool from standby, and a sick probe removing the standby machine from the list as usual.
func TestStandbyLifecycle(t *testing.T) {
	db := newDB(t)
	n := seedNode(t, db, "sb-1", "entry", true)

	// Mark standby.
	if _, err := MarkStandby(db, n.ID); err != nil {
		t.Fatal(err)
	}
	if got := getPoolState(t, db, n.ID); got.PoolState != StateStandby || got.PoolReason != "标记预备" {
		t.Fatalf("after mark = %+v", got)
	}

	// Already standby: re-marking reports ErrNotActive.
	if _, err := MarkStandby(db, n.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("second mark err = %v", err)
	}

	// Standby machine probes sick: removed from the list as usual (broken machines do not stay on standby).
	report(t, db, n.ID, "sick", time.Minute)
	report(t, db, n.ID, "sick", time.Minute)
	report(t, db, n.ID, "sick", time.Minute)
	if _, err := Sweep(db, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := getPoolState(t, db, n.ID); got.PoolState != StateSuspended {
		t.Fatalf("sick standby must be suspended: %+v", got)
	}

	// A suspended node cannot be marked standby directly (resume first).
	if _, err := MarkStandby(db, n.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("mark suspended err = %v", err)
	}

	// Standby return: resume back to active, mark standby again, then take the return branch.
	if _, err := Resume(db, n.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := MarkStandby(db, n.ID); err != nil {
		t.Fatal(err)
	}
	back, err := Resume(db, n.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if back.PoolState != StateActive || back.PoolReason != "预备回池" {
		t.Fatalf("after standby resume = %+v", back)
	}
}

// TestStandbyEntryPromote covers backfill picking: only online standby entries are picked,
// Promote lifts into the pool with a trace; offline standby machines are not picked.
func TestStandbyEntryPromote(t *testing.T) {
	db := newDB(t)
	offline := seedNode(t, db, "sb-off", "entry", true)
	if err := db.Model(&storage.Node{}).Where("id = ?", offline.ID).
		Updates(map[string]any{"pool_state": StateStandby, "status": "offline"}).Error; err != nil {
		t.Fatal(err)
	}
	online := seedNode(t, db, "sb-on", "entry", true)
	if err := db.Model(&storage.Node{}).Where("id = ?", online.ID).
		Updates(map[string]any{"pool_state": StateStandby, "status": "online"}).Error; err != nil {
		t.Fatal(err)
	}

	pick, err := StandbyEntry(db)
	if err != nil {
		t.Fatal(err)
	}
	if pick.ID != online.ID {
		t.Fatalf("picked = %+v, want online standby %d", pick, online.ID)
	}
	if err := Promote(db, pick.ID, "恢复补位"); err != nil {
		t.Fatal(err)
	}
	if got := getPoolState(t, db, pick.ID); got.PoolState != StateActive || got.PoolReason != "恢复补位" {
		t.Fatalf("after promote = %+v", got)
	}
	// Only the offline machine is left in the list after picking: nothing to pick.
	pick, err = StandbyEntry(db)
	if err != nil || pick.ID != 0 {
		t.Fatalf("second pick = %+v err=%v", pick, err)
	}
}
