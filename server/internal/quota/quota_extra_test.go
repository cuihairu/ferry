package quota

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestUsedBytes 覆盖 UsedBytes 无窗/有窗/扫描错误分支。
func TestUsedBytes(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { if s, _ := db.DB(); s != nil { _ = s.Close() } })
	now := time.Now()
	// 建一个用户，保证外键约束。
	u := storage.User{Username: "quota-used"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	// 无流量：返回 0。
	if got, err := UsedBytes(db, u.ID, CycleNone, now); err != nil || got != 0 {
		t.Fatalf("empty: %d %v", got, err)
	}
	// 落一行流量（无窗 cycle）。
	if err := db.Create(&storage.TrafficLog{UserID: u.ID, RxBytes: 30, TxBytes: 20, RecordedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if got, err := UsedBytes(db, u.ID, CycleNone, now); err != nil || got != 50 {
		t.Fatalf("no window: %d %v", got, err)
	}
	// 有窗（day）命中。
	if got, err := UsedBytes(db, u.ID, CycleDay, now); err != nil || got != 50 {
		t.Fatalf("day window: %d %v", got, err)
	}
	// 扫描错误：关闭库。
	s, _ := db.DB()
	_ = s.Close()
	if _, err := UsedBytes(db, u.ID, CycleNone, now); err == nil {
		t.Fatal("want error on closed db")
	}
}
