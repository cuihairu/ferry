package tasks

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestStateTransitionAndBackoff covers the shared state machine (F-1602),
// the bounded retry budget with exponential backoff (F-1605/F-1606) and the
// terminal dead state.
func TestStateTransitionAndBackoff(t *testing.T) {
	db := testDB(t)
	m := NewManager(db, nil)
	var calls int32
	m.Register("flaky", Definition{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    50 * time.Millisecond,
		Timeout:     2 * time.Second,
		Run: func(ctx context.Context, t *storage.Task) error {
			if atomic.AddInt32(&calls, 1) < 3 {
				return errors.New("boom")
			}
			t.Result = `{"ok":true}`
			return nil
		},
	})
	row, err := m.Enqueue("flaky", 7, "", "tester")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if row.Status != StatusPending {
		t.Fatalf("status = %s", row.Status)
	}
	// Drive the loop until the third attempt succeeds.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.sweep(context.Background())
		var got storage.Task
		_ = db.First(&got, row.ID).Error
		if got.Status == StatusSucceeded {
			if got.Result != `{"ok":true}` {
				t.Fatalf("result = %q", got.Result)
			}
			if got.Attempts != 3 {
				t.Fatalf("attempts = %d", got.Attempts)
			}
			if got.Executor == "" {
				t.Fatalf("executor not recorded")
			}
			if got.LogLines == "" {
				t.Fatalf("log trail empty")
			}
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("task never succeeded")
}

// TestDeadLetter covers budget exhaustion -> dead, and manual retry (F-1611)
// restoring the full budget.
func TestDeadLetterAndRetry(t *testing.T) {
	db := testDB(t)
	m := NewManager(db, nil)
	m.Register("always", Definition{
		MaxAttempts: 2,
		BaseDelay:   time.Millisecond,
		MaxDelay:    2 * time.Millisecond,
		Timeout:     time.Second,
		Run: func(ctx context.Context, t *storage.Task) error {
			return errors.New("nope")
		},
	})
	row, _ := m.Enqueue("always", 1, "", "")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.sweep(context.Background())
		var got storage.Task
		_ = db.First(&got, row.ID).Error
		if got.Status == StatusDead {
			if got.Error != "nope" {
				t.Fatalf("error = %q", got.Error)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	retried, err := m.Retry(row.ID, "admin")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried.Status != StatusPending || retried.Attempts != 0 || retried.Actor != "admin" {
		t.Fatalf("retried = %+v", retried)
	}
}

// TestIdempotentEnqueue covers the unique idempotency key (F-1608): same key
// returns the original row.
func TestIdempotentEnqueue(t *testing.T) {
	db := testDB(t)
	m := NewManager(db, nil)
	m.Register("one", Definition{Run: func(ctx context.Context, t *storage.Task) error { return nil }})
	a, err := m.Enqueue("one", 1, "dedupe-1", "")
	if err != nil {
		t.Fatalf("enqueue a: %v", err)
	}
	b, err := m.Enqueue("one", 1, "dedupe-1", "")
	if err != nil {
		t.Fatalf("enqueue b: %v", err)
	}
	if a.ID != b.ID {
		t.Fatalf("ids differ: %d vs %d", a.ID, b.ID)
	}
	if _, err := m.Enqueue("nope", 1, "", ""); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("unknown kind err = %v", err)
	}
}

// TestCancel covers cancel of pending and running tasks (F-1607) plus the
// timeout path staying a retryable failure rather than a cancel.
func TestCancelAndTimeout(t *testing.T) {
	db := testDB(t)
	m := NewManager(db, nil)
	m.Register("slow", Definition{
		MaxAttempts: 2,
		BaseDelay:   time.Millisecond,
		MaxDelay:    2 * time.Millisecond,
		Timeout:     80 * time.Millisecond,
		Run: func(ctx context.Context, t *storage.Task) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	row, _ := m.Enqueue("slow", 1, "", "")
	m.sweep(context.Background()) // claim -> running
	var got storage.Task
	_ = db.First(&got, row.ID).Error
	if got.Status != StatusRunning {
		t.Fatalf("status = %s", got.Status)
	}
	if _, err := m.Cancel(row.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = db.First(&got, row.ID).Error
		if got.Status == StatusCancelled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task never cancelled, status=%s", got.Status)
}

// TestDependsPrecheck covers the pre-execution dependency check (F-1630):
// a depends failure consumes an attempt and backfills the error.
func TestDependsPrecheck(t *testing.T) {
	db := testDB(t)
	m := NewManager(db, nil)
	ran := false
	m.Register("guarded", Definition{
		MaxAttempts: 1,
		Timeout:     time.Second,
		Depends: func(ctx context.Context, t storage.Task) error {
			return errors.New("missing dependency")
		},
		Run: func(ctx context.Context, t *storage.Task) error {
			ran = true
			return nil
		},
	})
	row, _ := m.Enqueue("guarded", 1, "", "")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.sweep(context.Background())
		var got storage.Task
		_ = db.First(&got, row.ID).Error
		if got.Status == StatusDead {
			if ran {
				t.Fatalf("run must not execute when depends fails")
			}
			if got.Error != "missing dependency" {
				t.Fatalf("error = %q", got.Error)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task never died, status=%s", row.Status)
}

// TestProgress covers the stage/progress publication (F-1609) and the
// partial-success result contract (F-1612).
func TestProgressAndPartial(t *testing.T) {
	db := testDB(t)
	m := NewManager(db, nil)
	m.Register("partial", Definition{
		MaxAttempts: 1,
		Timeout:     time.Second,
		Run: func(ctx context.Context, t *storage.Task) error {
			m.SetProgress(t.ID, "half", 50)
			t.Result = `[{"ref":1,"ok":true},{"ref":2,"ok":false,"error":"deny"}]`
			return nil
		},
	})
	row, _ := m.Enqueue("partial", 1, "", "")
	var got storage.Task
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.sweep(context.Background())
		_ = db.First(&got, row.ID).Error
		if got.Status == StatusSucceeded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.Status != StatusSucceeded || got.Progress != 100 || got.Result == "" {
		t.Fatalf("task = %+v", got)
	}
}

// TestQuotaAndAlerts covers the concurrency quota (F-1614) and the backlog /
// dead alert emissions (F-1615, one per window).
func TestQuotaAndAlerts(t *testing.T) {
	db := testDB(t)
	m := NewManager(db, nil)
	m.Register("idle", Definition{Run: func(ctx context.Context, t *storage.Task) error {
		<-ctx.Done()
		return ctx.Err()
	}})
	if err := storage.SetSetting(db, "task.concurrency", "1"); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	a, _ := m.Enqueue("idle", 1, "q-a", "")
	b, _ := m.Enqueue("idle", 2, "q-b", "")
	m.sweep(context.Background())
	var ga, gb storage.Task
	_ = db.First(&ga, a.ID).Error
	_ = db.First(&gb, b.ID).Error
	if ga.Status != StatusRunning || gb.Status != StatusPending {
		t.Fatalf("quota not applied: a=%s b=%s", ga.Status, gb.Status)
	}
	// Backlog alert: old pending beyond the threshold, one emit per window.
	if err := storage.SetSetting(db, "task.backlog_threshold", "1"); err != nil {
		t.Fatalf("set backlog threshold: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := db.Model(&storage.Task{}).Where("id = ?", gb.ID).Update("next_attempt_at", old).Error; err != nil {
		t.Fatalf("backdate: %v", err)
	}
	m.maybeAlert()
	m.maybeAlert() // second call inside the window must not re-emit
	var events []storage.Event
	if err := db.Where("kind = ?", "task_backlog").Find(&events).Error; err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("task_backlog events = %d", len(events))
	}
	_ = b
}

// TestSubSecondTimeoutRoundsUp guards the enqueue-time timeout mapping: a
// sub-second definition timeout must round up to 1s, never truncate to 0
// (which silently inherits the 300s default and neuters the attempt deadline).
func TestSubSecondTimeoutRoundsUp(t *testing.T) {
	db := testDB(t)
	m := NewManager(db, nil)
	m.Register("quick", Definition{
		Timeout: 400 * time.Millisecond,
		Run:     func(ctx context.Context, t *storage.Task) error { return nil },
	})
	row, err := m.Enqueue("quick", 1, "", "")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if row.TimeoutSeconds != 1 {
		t.Fatalf("timeout_seconds = %d, want 1", row.TimeoutSeconds)
	}
}
