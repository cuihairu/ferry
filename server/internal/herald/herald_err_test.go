package herald

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Backfill batch 23: herald's error-return policies - the sending side (URL parse/dial), the delivery-side
// transaction failure, and store and count query failures, plus in-loop error logging.
// The payload marshal-failure branch in http.go is left uncovered: outgoingEvent fields are all strings/ints/
// times and RawMessage, so a marshal error cannot normally be constructed (defensive code is not forced to fail).

// syncBuf is a thread-safe log buffer (Loop writes from a separate goroutine).
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitForLog polls until the log contains the expected string.
func waitForLog(t *testing.T, want string, s *syncBuf) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("log lacks %q: %s", want, s.String())
}

// deadAddr returns a local address that is guaranteed to be refused (a closed listening port).
func deadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listen: %v", err)
	}
	return addr
}

func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}

// failEventUpdates makes updates to event rows fail (markSent/markRetry both Update inside a transaction).
func failEventUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:fail_event_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Event); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

func testEvent(t *testing.T) storage.Event {
	t.Helper()
	return storage.Event{ID: 1, Kind: "cost_exceeded", Severity: SeverityCritical,
		Title: "事件", Target: "admin", Status: StatusPending, OccurredAt: time.Now()}
}

// TestHTTPSenderRequestErrors: request construction and dial failures on the sending side.
func TestHTTPSenderRequestErrors(t *testing.T) {
	// URL parse failure.
	if err := HTTPSender("http://bad url", "")(testEvent(t)); err == nil {
		t.Fatal("want URL parse error")
	}
	// Dial failure.
	if err := HTTPSender("http://"+deadAddr(t), "")(testEvent(t)); err == nil {
		t.Fatal("want dial error")
	}
}

// TestEmitCountDBErrors: how store and count errors surface when the DB is unavailable.
func TestEmitCountDBErrors(t *testing.T) {
	db := newHeraldDB(t)
	closeDB(t, db)
	if _, err := Emit(db, EmitInput{Kind: "recovery_failed", Severity: SeverityCritical,
		Title: "事件", Target: "admin"}); err == nil {
		t.Fatal("want emit create error")
	}
	if _, _, _, err := Count(db); err == nil {
		t.Fatal("want count query error")
	}
}

// TestDeliverErrorPaths: the delivery error policies - a nil sender short-circuits, the due-query fails,
// the success-mark update fails, and the failure scheduling fails.
func TestDeliverErrorPaths(t *testing.T) {
	now := time.Now()

	// Nil sender: just spins and returns.
	db := newHeraldDB(t)
	n, err := Deliver(db, now, nil)
	if err != nil || n != 0 {
		t.Fatalf("Deliver(nil) = %d %v", n, err)
	}

	// Due-query failure.
	db = newHeraldDB(t)
	emitOne(t, db, "cost_exceeded")
	closeDB(t, db)
	if _, err := Deliver(db, now, func(storage.Event) error { return nil }); err == nil {
		t.Fatal("want due query error")
	}

	// Success-mark failure (the markSent in-transaction update fails).
	db = newHeraldDB(t)
	emitOne(t, db, "cost_exceeded")
	failEventUpdates(t, db)
	if _, err := Deliver(db, now, func(storage.Event) error { return nil }); err == nil {
		t.Fatal("want markSent error")
	}

	// Failure scheduling failure (sender errors and the markRetry in-transaction update fails).
	db = newHeraldDB(t)
	emitOne(t, db, "cost_exceeded")
	failEventUpdates(t, db)
	boom := errors.New("channel down")
	if _, err := Deliver(db, now, func(storage.Event) error { return boom }); err == nil {
		t.Fatal("want markRetry error")
	}
}

// TestLoopLogsDeliverError: an in-loop delivery failure only logs and does not exit.
// Passing 0 for interval also covers the 30s fallback branch (the first round often runs).
func TestLoopLogsDeliverError(t *testing.T) {
	db := newHeraldDB(t)
	closeDB(t, db) // every round's due query fails

	buf := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 0, func(storage.Event) error { return nil }, log.New(buf, "", 0))
		close(done)
	}()
	waitForLog(t, "herald deliver:", buf)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not stop on cancel")
	}
}
