package save

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch 23 gap fill: the error-return conventions of save (watermark read/clear-rebuild/traffic query/accumulation/watermark
// write-back) and the in-loop scan-failure log.

// syncBuf is a thread-safe log buffer (Loop writes it from a separate goroutine).
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

// waitForLog polls until the expected string shows up in the log.
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

// drop drops a table: later statements against that table report no such table.
func drop(t *testing.T, db *gorm.DB, dest any) {
	t.Helper()
	if err := db.Migrator().DropTable(dest); err != nil {
		t.Fatalf("drop table: %v", err)
	}
}

// failSettingWrites makes writes to the watermark setting key fail (Save may go through the update or the create chain).
func failSettingWrites(t *testing.T, db *gorm.DB) {
	t.Helper()
	f := func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Setting); ok {
			tx.AddError(errors.New("db down"))
		}
	}
	db.Callback().Update().Before("gorm:update").Register("test:fail_setting_update", f)
	db.Callback().Create().Before("gorm:create").Register("test:fail_setting_create", f)
}

// setWatermark pre-seeds the watermark key (skipping the no-watermark clear-and-rebuild branch).
func setWatermark(t *testing.T, db *gorm.DB, v int64) {
	t.Helper()
	if err := storage.SetSetting(db, settingWatermark, strconv.FormatInt(v, 10)); err != nil {
		t.Fatalf("set watermark: %v", err)
	}
}

func traffic1() storage.NodeTrafficLog {
	return storage.NodeTrafficLog{
		NodeID: 1, DirectBytes: 100,
		RecordedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
	}
}

// TestSweepDBErrors covers five kinds of database failure in Sweep: watermark read failure, clear failure when there is no watermark, traffic
// query failure, accumulation failure, watermark write-back failure.
func TestSweepDBErrors(t *testing.T) {
	// Watermark read failure.
	db := newTestDB(t)
	drop(t, db, &storage.Setting{})
	if _, err := Sweep(db); err == nil {
		t.Fatal("want watermark read error")
	}

	// Clear-rebuild failure when there is no watermark.
	db = newTestDB(t)
	drop(t, db, &storage.SaveStat{})
	if _, err := Sweep(db); err == nil {
		t.Fatal("want delete error")
	}

	// Traffic query failure.
	db = newTestDB(t)
	drop(t, db, &storage.NodeTrafficLog{})
	if _, err := Sweep(db); err == nil {
		t.Fatal("want traffic query error")
	}

	// Accumulation failure: the watermark is already set (skipping the clear) and the traffic rows are read, but save_stats is missing.
	db = newTestDB(t)
	setWatermark(t, db, 0)
	seedTraffic(t, db, traffic1())
	drop(t, db, &storage.SaveStat{})
	if _, err := Sweep(db); err == nil {
		t.Fatal("want add error")
	}

	// Watermark write-back failure.
	db = newTestDB(t)
	seedTraffic(t, db, traffic1())
	failSettingWrites(t, db)
	if _, err := Sweep(db); err == nil {
		t.Fatal("want watermark write error")
	}
}

// TestUserSavingsDBErrors covers the failure convention of each of the two conversion queries.
func TestUserSavingsDBErrors(t *testing.T) {
	db := newTestDB(t)
	drop(t, db, &storage.SaveStat{})
	if _, _, _, err := UserSavings(db, 1, "2026-10-01"); err == nil {
		t.Fatal("want save_stats query error")
	}

	db = newTestDB(t)
	drop(t, db, &storage.TrafficLog{})
	if _, _, _, err := UserSavings(db, 1, "2026-10-01"); err == nil {
		t.Fatal("want traffic_logs query error")
	}
}

// TestLoopLogsSweepError a scan failure inside the loop is only logged and does not exit.
func TestLoopLogsSweepError(t *testing.T) {
	db := newTestDB(t)
	drop(t, db, &storage.SaveStat{}) // no watermark + missing table: clear-and-rebuild fails every round

	buf := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 5*time.Millisecond, log.New(buf, "", 0))
		close(done)
	}()
	waitForLog(t, "save stats sweep:", buf)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not stop on cancel")
	}
}

// TestLoopDefaultsInterval interval<=0 falls back to the 30s default: the first round still runs as usual.
func TestLoopDefaultsInterval(t *testing.T) {
	db := newTestDB(t)
	seedTraffic(t, db, traffic1())

	buf := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 0, log.New(buf, "", 0))
		close(done)
	}()
	waitForLog(t, "save stats: 1 traffic row(s) aggregated", buf)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not stop on cancel")
	}
}
