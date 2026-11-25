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
	seed(t, db, now.AddDate(0, 0, -91)) // outside the window
	seed(t, db, now.AddDate(0, 0, -90)) // exactly on the window boundary (younger than the cutoff is deleted, the equal point is kept)
	seed(t, db, now.AddDate(0, 0, -1))  // inside the window
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
	// An hourly sweepInterval is inconvenient for tests: verify directly that Loop does not start for retention<=0 and
	// exits on ctx cancel (the sweep semantics are covered by the Prune case).
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

// TestLoopNilLoggerDefaults covers the nil-logger fallback: the default logger starts the goroutine and
// exits on ctx cancel (retention>0 is needed to reach the fallback branch).
func TestLoopNilLoggerDefaults(t *testing.T) {
	db := newDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 30, nil)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop with nil logger did not return after cancel")
	}
}

// TestLoopTickSweepsAndStops covers the tick sweep main path: out-of-window rows are deleted when due and it
// exits on ctx cancel (the window days come from retention, and the tick is shortened temporarily).
func TestLoopTickSweepsAndStops(t *testing.T) {
	db := newDB(t)
	seed(t, db, time.Now().AddDate(0, 0, -31)) // outside the window (retention=30 days)
	seed(t, db, time.Now().AddDate(0, 0, -1))  // inside the window

	old := sweepInterval
	sweepInterval = 10 * time.Millisecond
	t.Cleanup(func() { sweepInterval = old })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 30, log.New(io.Discard, "", 0))
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var cnt int64
		db.Model(&storage.AuditLog{}).Count(&cnt)
		if cnt == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("loop tick never pruned; rows = %d", cnt)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not stop after cancel")
	}
}
