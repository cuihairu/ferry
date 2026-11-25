package cost

import (
	"bytes"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/costref"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func seedWatch(t *testing.T, db *gorm.DB, provider, region, spec string, target int64) storage.PriceWatch {
	t.Helper()
	w := storage.PriceWatch{Provider: provider, Region: region, Spec: spec, TargetPrice: target, Enabled: true}
	if err := db.Create(&w).Error; err != nil {
		t.Fatal(err)
	}
	return w
}

func priceEvents(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindPriceAlert).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCheckPriceWatches covers the price watch scan: the baseline does not alert, the same price does not,
// a drop and a target hit in the same round each emit one, staying put does not repeat (same-day dedup + target only on a crossing),
// an unimported table is a no-op round, and disabled watches are not scanned.
func TestCheckPriceWatches(t *testing.T) {
	db := newTestDB(t)
	logger := log.New(&bytes.Buffer{}, "", 0)
	now := time.Now()

	// No price table imported: the whole round is a no-op.
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("no table scan: %v", err)
	}

	if err := SaveRefTable(db, []byte(`[{"provider":"vultr","region":"tokyo","spec":"1c","monthly_cents":1000}]`)); err != nil {
		t.Fatal(err)
	}
	w := seedWatch(t, db, "vultr", "tokyo", "1c", 900)

	// Round 1: baseline snapshot; no previous snapshot means no alert.
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	var snaps []storage.PriceSnapshot
	if err := db.Where("watch_id = ?", w.ID).Find(&snaps).Error; err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].MonthlyCents != 1000 {
		t.Fatalf("snapshots after scan1 = %+v", snaps)
	}
	if n := priceEvents(t, db); n != 0 {
		t.Fatalf("scan1 events = %d", n)
	}

	// Round 2: same price — no drop, target not reached.
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if n := priceEvents(t, db); n != 0 {
		t.Fatalf("scan2 events = %d", n)
	}

	// Round 3: drops to 800 (-20%, and past the target price 900) — a drop and a hit, one each.
	if err := SaveRefTable(db, []byte(`[{"provider":"vultr","region":"tokyo","spec":"1c","monthly_cents":800}]`)); err != nil {
		t.Fatal(err)
	}
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 3: %v", err)
	}
	if n := priceEvents(t, db); n != 2 {
		t.Fatalf("scan3 events = %d, want 2", n)
	}

	// Round 4: the price holds at 800 — no repeated drop, and no repeat hit (not a crossing).
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 4: %v", err)
	}
	if n := priceEvents(t, db); n != 2 {
		t.Fatalf("scan4 events = %d, want still 2", n)
	}

	// After disabling the watch, no more snapshots are scanned.
	db.Model(&storage.PriceWatch{}).Where("id = ?", w.ID).Update("enabled", false)
	if err := SaveRefTable(db, []byte(`[{"provider":"vultr","region":"tokyo","spec":"1c","monthly_cents":500}]`)); err != nil {
		t.Fatal(err)
	}
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 5: %v", err)
	}
	var cnt int64
	db.Model(&storage.PriceSnapshot{}).Where("watch_id = ?", w.ID).Count(&cnt)
	if cnt != 4 {
		t.Fatalf("snapshots = %d, want 4 (disabled watch not scanned)", cnt)
	}

	// The key is not in the table: silently skipped, no snapshot and no alert.
	seedWatch(t, db, "nosuch", "x", "y", 0)
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 6: %v", err)
	}
	var ev int64
	db.Model(&storage.Event{}).Where("kind = ?", herald.KindPriceAlert).Count(&ev)
	if ev != 2 {
		t.Fatalf("events after miss scan = %d", ev)
	}
}

// TestQuoteURL covers the price-page annotation: a missing URL is labeled explicitly.
func TestQuoteURL(t *testing.T) {
	if got := quoteURL(costref.Quote{}); got != "（未附）" {
		t.Errorf("empty URL = %q; want （未附）", got)
	}
	if got := quoteURL(costref.Quote{URL: "https://price"}); got != "https://price" {
		t.Errorf("URL = %q", got)
	}
}

// TestEmitPriceAlertBranches covers same-day dedup and event-table failure tolerance.
func TestEmitPriceAlertBranches(t *testing.T) {
	db := newTestDB(t)
	emitPriceAlert(db, "t1", "b1", "d1")
	emitPriceAlert(db, "t1", "b1", "d1") // same dedup already stored: skip
	if got := priceEvents(t, db); got != 1 {
		t.Fatalf("events = %d; want 1 (dedup)", got)
	}
	emitPriceAlert(db, "t2", "b2", "d2") // a different dedup: sent as usual
	if got := priceEvents(t, db); got != 2 {
		t.Fatalf("events = %d; want 2", got)
	}
	// The event table is unwritable: only logged, no panic.
	if err := db.Migrator().DropTable(&storage.Event{}); err != nil {
		t.Fatal(err)
	}
	emitPriceAlert(db, "t3", "b3", "d3")
}

// TestCheckPriceWatchesDBError covers an unavailable DB surfacing immediately; a nil logger falls back to the default.
func TestCheckPriceWatchesDBError(t *testing.T) {
	db := newTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CheckPriceWatches(db, time.Now(), nil); err == nil {
		t.Fatal("closed db must fail")
	}
}
