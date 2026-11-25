package review

import (
	"bytes"
	"context"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch 22 coverage: review DB failures surfacing, in-loop errors only logged, and outbound
// channel fault tolerance after disabling.

// openDB opens a file-backed database and migrates it (same criteria as the existing inline DB setup).
func openDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "review.db"))
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

// closeSQL closes the underlying connection so all later DB operations error.
func closeSQL(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}

// bufLogger is a thread-safe log buffer (Run writes from a separate goroutine).
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

// withDefaultLog points the default logger at the buffer (Run has no logger injection point, only
// log.Printf; no tests run in parallel, stderr is restored afterwards).
func withDefaultLog(t *testing.T, l *bufLogger) {
	t.Helper()
	log.SetOutput(l)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
}

// waitForLog polls until the expected string appears in the log (avoids sleeps hanging the case).
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

// deadAddr returns a local address that must be refused (an already-closed listen port).
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

// TestDisableInactiveDBError covers immediate failure on the first query: no disable count, the error surfaces directly.
func TestDisableInactiveDBError(t *testing.T) {
	db := openDB(t)
	closeSQL(t, db)
	if n, err := DisableInactive(db, time.Now()); err == nil {
		t.Fatalf("want error, got n=%d", n)
	}
}

// TestDisableInactiveOverQuotaError covers expiry disabling succeeding while the over-quota pass
// fails (drop the traffic table: the over-quota subquery references it).
func TestDisableInactiveOverQuotaError(t *testing.T) {
	db := openDB(t)
	past := time.Now().Add(-24 * time.Hour)
	if err := db.Create(&storage.User{
		Username: "expired", SubToken: "t-err", Enabled: true, ExpiresAt: &past,
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.Migrator().DropTable(&storage.TrafficLog{}); err != nil {
		t.Fatalf("drop traffic_logs: %v", err)
	}
	if _, err := DisableInactive(db, time.Now()); err == nil {
		t.Fatal("want error from over-quota pass")
	}
}

// TestRunLogsDisableError covers an in-loop disable failure being only logged, without exiting the loop.
func TestRunLogsDisableError(t *testing.T) {
	db := openDB(t)
	closeSQL(t, db)
	l := &bufLogger{}
	withDefaultLog(t, l)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Run(ctx, db, 5*time.Millisecond)
		close(done)
	}()
	waitForLog(t, "review users:", l)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}

// TestRunLogsNotifyFailure covers a nonzero disable count with an outbound channel fault: the
// alert is only logged; the disabling itself already took effect.
func TestRunLogsNotifyFailure(t *testing.T) {
	db := openDB(t)
	past := time.Now().Add(-24 * time.Hour)
	if err := db.Create(&storage.User{
		Username: "expired", SubToken: "t-notify", Enabled: true, ExpiresAt: &past,
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := storage.SetSetting(db, notify.KeyURL, "http://"+deadAddr(t)); err != nil {
		t.Fatalf("set webhook: %v", err)
	}

	l := &bufLogger{}
	withDefaultLog(t, l)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Run(ctx, db, 5*time.Millisecond)
		close(done)
	}()
	waitForLog(t, "notify webhook:", l)
	if !strings.Contains(l.String(), "disabled 1") {
		t.Fatalf("log lacks disabled count: %s", l.String())
	}
	cancel()
	<-done
}
