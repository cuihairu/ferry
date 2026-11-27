package cost

import (
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCheckGrowth covers F-1831: a traffic-billed node whose month-to-date
// daily pace spikes past the trailing 7-day average lands one cost_growth
// warning per month; an even-pace node, a noise-floor baseline and a
// fixed-billing node stay quiet; the month dedup keeps a re-check silent.
func TestCheckGrowth(t *testing.T) {
	db := newTestDB(t)
	// Fixed mid-month clock: 15 days elapsed.
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	dayStart := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	grow := seedNode(t, db, "g-grow", "hkg", "按流量", 100, 0)
	flat := seedNode(t, db, "g-flat", "hkg", "按流量", 100, 0)
	tiny := seedNode(t, db, "g-tiny", "hkg", "按流量", 100, 0)
	fixed := seedNode(t, db, "g-fixed", "hkg", "包月", 100, 9900)

	const GB = int64(1_000_000_000)
	// The month runs at each node's steady pace; today g-grow spikes 100x
	// (the baseline week overlaps the month's recent days by design, so the
	// spike must come from today to stay a clean 5x+ over the average).
	for d := 14; d >= 1; d-- {
		seedTraffic(t, db, grow.ID, GB/5, 0, dayStart.AddDate(0, 0, -d).Add(time.Hour))
		seedTraffic(t, db, flat.ID, GB, 0, dayStart.AddDate(0, 0, -d).Add(time.Hour))
		seedTraffic(t, db, tiny.ID, 300*1024, 0, dayStart.AddDate(0, 0, -d).Add(time.Hour))
		seedTraffic(t, db, fixed.ID, 4*GB, 0, dayStart.AddDate(0, 0, -d).Add(time.Hour))
	}
	seedTraffic(t, db, grow.ID, 20*GB, 0, dayStart.Add(2*time.Hour))
	seedTraffic(t, db, flat.ID, GB, 0, dayStart.Add(2*time.Hour))
	seedTraffic(t, db, tiny.ID, 3*1024*1024, 0, dayStart.Add(2*time.Hour))
	seedTraffic(t, db, fixed.ID, 40*GB, 0, dayStart.Add(2*time.Hour))

	logger := log.New(io.Discard, "", 0)
	if err := CheckGrowth(db, now, logger); err != nil {
		t.Fatal(err)
	}
	var evs []storage.Event
	if err := db.Where("kind = ?", herald.KindCostGrowth).Order("id").Find(&evs).Error; err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("cost_growth events = %d, want 1 (only g-grow)", len(evs))
	}
	if evs[0].DedupKey != fmt.Sprintf("node:%d:cost_growth:202606", grow.ID) {
		t.Fatalf("dedup key = %q", evs[0].DedupKey)
	}
	if evs[0].Target != herald.TargetAdmin || evs[0].Severity != herald.SeverityWarning {
		t.Fatalf("event shape: %+v", evs[0])
	}

	// Month dedup: a re-check inside the same month stays silent.
	if err := CheckGrowth(db, now, logger); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindCostGrowth).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("same-month re-check must dedup: %d events", n)
	}
}
