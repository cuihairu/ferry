package notifyscan

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch-22 coverage: the error-return rules of each notifyscan path, the traffic-warning threshold fallback and the error log inside the loop.

func dropTable(t *testing.T, db *gorm.DB, dest any) {
	t.Helper()
	if err := db.Migrator().DropTable(dest); err != nil {
		t.Fatalf("drop table: %v", err)
	}
}

// failCreates makes every later insert fail (gorm create callback error injection).
func failCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:fail_create", func(tx *gorm.DB) {
		tx.AddError(errors.New("db down"))
	})
}

// bufLogger is a thread-safe log buffer (Loop writes from its own goroutine).
type bufLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *bufLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *bufLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// waitForLog polls until the expected string shows up in the log.
func waitForLog(t *testing.T, want string, l *bufLogger) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(l.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("log lacks %q: %s", want, l.String())
}

// seedExpiringUser creates a user expiring within 7 days with the expiry preference on.
func seedExpiringUser(t *testing.T, db *gorm.DB) storage.User {
	t.Helper()
	return seedUser(t, db, storage.User{
		Username: "exp-err", SubToken: "e1", Enabled: true, NotifyExpiry: true,
		ExpiresAt: at(-1),
	})
}

// seedTrafficUser creates a user with the traffic preference on and usage at usedPct%;
// passing an out-of-range warnPercent (>100) exercises the fallback rule.
func seedTrafficUser(t *testing.T, db *gorm.DB, warnPercent int, usedPct int64) storage.User {
	t.Helper()
	gb := int64(1) << 30
	u := seedUser(t, db, storage.User{
		Username: "tr-err", SubToken: "q1", QuotaBytes: gb, ResetCycle: "none",
		Enabled: true, NotifyTraffic: true, TrafficWarnPercent: warnPercent,
	})
	now := time.Now()
	uid := u.ID
	if err := db.Create(&storage.TrafficLog{
		UserID: &uid, RxBytes: gb * usedPct / 100, RecordedAt: now,
	}).Error; err != nil {
		t.Fatalf("traffic seed: %v", err)
	}
	return u
}

// TestSweepExpiryDBErrors covers three expiry-sweep failures: the candidate query,
// the same-day dedup query, and the in-app notification write.
func TestSweepExpiryDBErrors(t *testing.T) {
	// The candidate-user query fails.
	db := newTestDB(t)
	dropTable(t, db, &storage.User{})
	if err := sweepExpiry(db, time.Now()); err == nil {
		t.Fatal("want users query error")
	}

	// The same-day dedup query fails (the candidate user exists, the notification table is missing).
	db = newTestDB(t)
	seedExpiringUser(t, db)
	dropTable(t, db, &storage.Notification{})
	if err := sweepExpiry(db, time.Now()); err == nil {
		t.Fatal("want dedup query error")
	}

	// The in-app notification write fails.
	db = newTestDB(t)
	seedExpiringUser(t, db)
	failCreates(t, db)
	if err := sweepExpiry(db, time.Now()); err == nil {
		t.Fatal("want notification create error")
	}
}

// TestExpiryTextFutureDays: future expiry wording takes the "due in N days" branch.
func TestExpiryTextFutureDays(t *testing.T) {
	now := time.Now()
	text := expiryText(now.Add(3*24*time.Hour), now)
	if !strings.Contains(text, "3 天后到期") {
		t.Fatalf("text = %q, want 3 天后到期", text)
	}
}

// TestSweepTrafficDBErrors covers five traffic-sweep rules: the candidate query, the dedup query,
// the usage query, the out-of-range threshold falling back to 80, and the notification write.
func TestSweepTrafficDBErrors(t *testing.T) {
	// The candidate-user query fails.
	db := newTestDB(t)
	dropTable(t, db, &storage.User{})
	if err := sweepTraffic(db, time.Now()); err == nil {
		t.Fatal("want users query error")
	}

	// The same-day dedup query fails.
	db = newTestDB(t)
	seedTrafficUser(t, db, 80, 90)
	dropTable(t, db, &storage.Notification{})
	if err := sweepTraffic(db, time.Now()); err == nil {
		t.Fatal("want dedup query error")
	}

	// The usage query fails (the traffic table is missing).
	db = newTestDB(t)
	seedTrafficUser(t, db, 80, 90)
	dropTable(t, db, &storage.TrafficLog{})
	if err := sweepTraffic(db, time.Now()); err == nil {
		t.Fatal("want usage query error")
	}

	// Out-of-range threshold falls back to 80: 90% usage still warns.
	db = newTestDB(t)
	seedTrafficUser(t, db, 200, 90)
	if err := sweepTraffic(db, time.Now()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var rows []storage.Notification
	if err := db.Where("type = ?", storage.NotifTraffic).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	// Without the fallback (warn=200) 89% would not warn, so any notification proves the fallback ran.
	if len(rows) != 1 || !strings.Contains(rows[0].Title, "流量预警") {
		t.Fatalf("traffic notifications = %d, want one clamped warn", len(rows))
	}

	// The in-app notification write fails.
	db = newTestDB(t)
	seedTrafficUser(t, db, 80, 90)
	failCreates(t, db)
	if err := sweepTraffic(db, time.Now()); err == nil {
		t.Fatal("want notification create error")
	}
}

// TestLoopLogsSweepError: a sweep failure inside the loop only logs and does not exit.
func TestLoopLogsSweepError(t *testing.T) {
	db := newTestDB(t)
	dropTable(t, db, &storage.User{})

	l := &bufLogger{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 5*time.Millisecond, log.New(l, "", 0))
		close(done)
	}()
	waitForLog(t, "notifyscan:", l)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not stop on cancel")
	}
}
