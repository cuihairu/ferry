package pool

import (
	"errors"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestStandbyLifecycle 覆盖预备清单（BR-3）：在池入口标记预备（出订阅但
// 在册待命）、摘除态/预备态不可重复标记、预备态可手动回池、探测 sick
// 照常把预备机摘出清单。
func TestStandbyLifecycle(t *testing.T) {
	db := newDB(t)
	n := seedNode(t, db, "sb-1", "entry", true)

	// 标记预备。
	if _, err := MarkStandby(db, n.ID); err != nil {
		t.Fatal(err)
	}
	if got := getPoolState(t, db, n.ID); got.PoolState != StateStandby || got.PoolReason != "标记预备" {
		t.Fatalf("after mark = %+v", got)
	}

	// 已是预备：再标记报 ErrNotActive。
	if _, err := MarkStandby(db, n.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("second mark err = %v", err)
	}

	// 预备机探测 sick：照常摘除出清单（坏机不留待命）。
	report(t, db, n.ID, "sick", time.Minute)
	report(t, db, n.ID, "sick", time.Minute)
	report(t, db, n.ID, "sick", time.Minute)
	if _, err := Sweep(db, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := getPoolState(t, db, n.ID); got.PoolState != StateSuspended {
		t.Fatalf("sick standby must be suspended: %+v", got)
	}

	// 摘除态不可直接标记预备（须先复位）。
	if _, err := MarkStandby(db, n.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("mark suspended err = %v", err)
	}

	// 预备回池：先复位回 active，再标记预备，再走回池分支。
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

// TestStandbyEntryPromote 覆盖补位取机：只取在线的预备入口，Promote 升池
// 留痕；离线预备机不会被取用。
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
	// 取用后清单里只剩离线机：取不到。
	pick, err = StandbyEntry(db)
	if err != nil || pick.ID != 0 {
		t.Fatalf("second pick = %+v err=%v", pick, err)
	}
}
