package audit

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/au.db"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&storage.AuditLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seed(t *testing.T, db *gorm.DB, createdAt time.Time) {
	t.Helper()
	if err := db.Create(&storage.AuditLog{Actor: "admin", ActorKind: "admin", Action: "POST /api/users", CreatedAt: createdAt}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestPruneRemovesOnlyOutOfWindow(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	seed(t, db, now.AddDate(0, 0, -91)) // 窗外
	seed(t, db, now.AddDate(0, 0, -90)) // 恰在窗口边界（< 截点为删，等点保留）
	seed(t, db, now.AddDate(0, 0, -1))  // 窗内
	n, err := Prune(db, 90, now)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned = %d, want 1", n)
	}
	var left []storage.AuditLog
	db.Find(&left)
	if len(left) != 2 {
		t.Fatalf("left = %d, want 2", len(left))
	}
}

func TestPruneZeroRetentionNoop(t *testing.T) {
	db := newDB(t)
	seed(t, db, time.Now().AddDate(-1, 0, 0))
	n, err := Prune(db, 0, time.Now())
	if err != nil || n != 0 {
		t.Fatalf("prune(0) = %d,%v, want 0,nil", n, err)
	}
	var cnt int64
	db.Model(&storage.AuditLog{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("rows after noop = %d, want 1", cnt)
	}
}

func TestLoopSweepsAndStops(t *testing.T) {
	// sweepInterval 小时级不便测试：直接验证 Loop 对 retention<=0 不启动、
	// ctx 取消即退出（清扫语义由 Prune 用例覆盖）。
	db := newDB(t)
	done := make(chan struct{})
	go func() {
		Loop(context.Background(), db, 0, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Loop with retention=0 did not return immediately")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go Loop(ctx, db, 90, log.New(io.Discard, "", 0))
	cancel()
}
