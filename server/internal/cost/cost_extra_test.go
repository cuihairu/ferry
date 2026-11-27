package cost

import (
	"bytes"
	"context"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestGroupEmptyKey covers empty region/ISP: grouped under the "unknown" key instead of an empty key.
func TestGroupEmptyKey(t *testing.T) {
	by := map[string]*GroupCost{}
	group(by, "", NodeCost{TrafficCostCents: 1, FixedCostCents: 2, ProjectedCents: 3}, 0, false)
	g, ok := by["未知"]
	if !ok || g.Nodes != 1 || g.TrafficCents != 1 || g.FixedCents != 2 || g.ProjectedCents != 3 {
		t.Fatalf("empty key must map to 未知: %#v", by)
	}
}

// TestGetThresholdDirtyValue covers a dirty stored threshold (non-numeric/negative): falls back to the default without error.
func TestGetThresholdDirtyValue(t *testing.T) {
	db := newTestDB(t)
	for _, dirty := range []string{"abc", "-5"} {
		if err := storage.SetSetting(db, settingThreshold, dirty); err != nil {
			t.Fatal(err)
		}
		cents, err := GetThreshold(db)
		if err != nil || cents != DefaultThresholdCents {
			t.Fatalf("dirty %q: cents=%d err=%v; want default", dirty, cents, err)
		}
	}
}

// TestBuildOnBrokenDB covers a closed underlying connection: Build/CheckAlerts propagate the
// error as-is while Loop logs it and continues to the next round.
func TestBuildOnBrokenDB(t *testing.T) {
	db := newTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(db, time.Now()); err == nil {
		t.Fatal("build on closed db must error")
	}
	if err := CheckAlerts(db, time.Now(), log.New(io.Discard, "", 0)); err == nil {
		t.Fatal("check alerts on closed db must error")
	}

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	Loop(ctx, db, 0, log.New(&buf, "", 0)) // interval<=0 falls back to the default; check failures only log
	if !strings.Contains(buf.String(), "cost check") {
		t.Fatalf("loop error not logged: %q", buf.String())
	}
}

// TestCheckAlertsNotifiesWebhook covers webhook delivery when an over-threshold node's alert
// activates: a send failure only logs and does not affect the alert itself.
func TestCheckAlertsNotifiesWebhook(t *testing.T) {
	db := newTestDB(t)
	n := seedNode(t, db, "costly", "hk", "按流量", 1000, 0)    // 1000 cents/GB
	seedTraffic(t, db, n.ID, 60_000_000_000, 0, time.Now()) // 60GB → 60000 cents > default 5000
	if err := storage.SetSetting(db, notify.KeyURL, "http://127.0.0.1:1/hook"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := CheckAlerts(db, time.Now(), log.New(&buf, "", 0)); err != nil {
		t.Fatalf("check alerts: %v", err)
	}
	if !strings.Contains(buf.String(), "cost alert") {
		t.Fatalf("alert not logged: %q", buf.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "notify webhook") {
		if time.Now().After(deadline) {
			t.Fatalf("webhook failure not logged: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
