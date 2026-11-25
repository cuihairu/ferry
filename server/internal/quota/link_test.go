package quota

import (
	"context"
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newLinkDB(t *testing.T) *gorm.DB {
	t.Helper()
	// Isolate in-memory DBs by test name + timestamp: file::memory:?cache=shared shares one DB across the
	// package, so leftover users/logs would cross-contaminate linkage evaluation, and -count=2 reruns would hit stale data.
	dsn := fmt.Sprintf("file:link-%s-%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&storage.User{}, &storage.Node{}, &storage.TrafficLog{},
		&storage.Notification{}, &storage.QuotaAction{}, &storage.Setting{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// seedLinkUser note: the want map must be built before Create — fields with GORM default tags are
// backfilled into the struct via RETURNING after Create, so values read afterwards may not be the intended ones.
func seedLinkUser(t *testing.T, db *gorm.DB, name string, quotaBytes int64) storage.User {
	t.Helper()
	u := storage.User{Username: name, SubToken: "tok-" + name, QuotaBytes: quotaBytes, Enabled: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

func TestLinkSetting(t *testing.T) {
	db := newLinkDB(t)

	// Unset falls back to default: off, traffic tier 90%
	got, err := LoadLinkSetting(db)
	if err != nil || got.Enabled || got.TrafficPercent != DefaultTrafficPercent {
		t.Fatalf("default setting = %+v err=%v", got, err)
	}

	// Save and read back
	want := LinkSetting{Enabled: true, TrafficPercent: 95, CostCents: 2000, MaxPriceCents: 500}
	if err := SaveLinkSetting(db, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err = LoadLinkSetting(db)
	if err != nil || got != want {
		t.Fatalf("roundtrip = %+v err=%v, want %+v", got, err, want)
	}

	// Enabled with both tier triggers off: reject
	if err := SaveLinkSetting(db, LinkSetting{Enabled: true}); err == nil {
		t.Fatal("enabled with no trigger should fail")
	}
	// Out-of-range normalized: percent 300 → 90, negatives to 0
	if err := SaveLinkSetting(db, LinkSetting{Enabled: true, TrafficPercent: 300, CostCents: -1, MaxPriceCents: -5}); err != nil {
		t.Fatalf("save out-of-range: %v", err)
	}
	got, _ = LoadLinkSetting(db)
	if got.TrafficPercent != DefaultTrafficPercent || got.CostCents != 0 || got.MaxPriceCents != 0 {
		t.Fatalf("normalize = %+v", got)
	}

	// Corrupt data follows the default
	if err := db.Save(&storage.Setting{Key: settingKey, Value: "{oops"}).Error; err != nil {
		t.Fatal(err)
	}
	got, err = LoadLinkSetting(db)
	if err != nil || got.Enabled {
		t.Fatalf("corrupt setting should fall back to default, got %+v err=%v", got, err)
	}
}

func TestLinkTrigger(t *testing.T) {
	s := LinkSetting{Enabled: true, TrafficPercent: 90, CostCents: 1000}

	if got := s.Trigger(899, 1000, 0); got != "" {
		t.Fatalf("89%% should not trigger, got %q", got)
	}
	if got := s.Trigger(900, 1000, 0); got != LinkTriggerTraffic {
		t.Fatalf("90%% should trigger traffic, got %q", got)
	}
	// Quota 0 (unlimited) never triggers the traffic tier, but the cost tier can
	if got := s.Trigger(1<<40, 0, 500); got != "" {
		t.Fatalf("quota=0 should skip traffic tier, got %q", got)
	}
	if got := s.Trigger(1<<40, 0, 1000); got != LinkTriggerCost {
		t.Fatalf("cost tier should trigger, got %q", got)
	}
	// Both tiers matched reports the traffic tier
	if got := s.Trigger(900, 1000, 2000); got != LinkTriggerTraffic {
		t.Fatalf("traffic tier wins, got %q", got)
	}
	// Disabled never triggers
	off := s
	off.Enabled = false
	if got := off.Trigger(1<<40, 1000, 1<<40); got != "" {
		t.Fatalf("disabled should never trigger, got %q", got)
	}
}

func TestLinkLowCost(t *testing.T) {
	s := LinkSetting{MaxPriceCents: 500}
	flat := storage.Node{BillingType: "包月"}
	cheap := storage.Node{BillingType: "按流量", TrafficPriceCents: 500}
	pricey := storage.Node{BillingType: "按流量", TrafficPriceCents: 501}

	if !s.LowCost(flat) || !s.LowCost(cheap) {
		t.Fatal("包月与档线内节点应为低成本档")
	}
	if s.LowCost(pricey) {
		t.Fatal("超档线按流量节点不应算低成本档")
	}
	// Tier line 0: only monthly counts as low-cost tier
	if (LinkSetting{}).LowCost(cheap) {
		t.Fatal("档线 0 时按流量节点不应算低成本档")
	}
}

func seedTraffic(t *testing.T, db *gorm.DB, userID uint, nodeID *uint, rx, tx int64, at time.Time) {
	t.Helper()
	if err := db.Create(&storage.TrafficLog{UserID: &userID, NodeID: nodeID, RxBytes: rx, TxBytes: tx, RecordedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestWindowCostCents(t *testing.T) {
	db := newLinkDB(t)
	u := seedLinkUser(t, db, "costly", 0)
	flat := storage.Node{Name: "flat", Token: "tok-flat", BillingType: "包月", TrafficPriceCents: 0}
	pricey := storage.Node{Name: "pricey", Token: "tok-pricey", BillingType: "按流量", TrafficPriceCents: 300}
	if err := db.Create(&flat).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&pricey).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// pricey node 2GB (rx+tx) × 300 cents/GB = 600 cents; monthly node not counted
	seedTraffic(t, db, u.ID, &pricey.ID, 1<<30, 1<<30, now)
	seedTraffic(t, db, u.ID, &flat.ID, 1<<30, 1<<30, now)
	// Same volume last month: outside the month window, not counted
	seedTraffic(t, db, u.ID, &pricey.ID, 1<<30, 1<<30, now.AddDate(0, -1, 0))

	cost, err := WindowCostCents(db, u.ID, CycleMonth, now)
	if err != nil {
		t.Fatalf("WindowCostCents: %v", err)
	}
	if cost != 600 {
		t.Fatalf("window cost = %d, want 600", cost)
	}
	// none accumulates everything: 600+600
	cost, err = WindowCostCents(db, u.ID, CycleNone, now)
	if err != nil || cost != 1200 {
		t.Fatalf("none-window cost = %d err=%v, want 1200", cost, err)
	}
}

func TestLinkSweepTrafficTier(t *testing.T) {
	db := newLinkDB(t)
	u := seedLinkUser(t, db, "heavy", 1000)
	setting := LinkSetting{Enabled: true, TrafficPercent: 90, MaxPriceCents: 500}
	if err := SaveLinkSetting(db, setting); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	seedTraffic(t, db, u.ID, nil, 900, 0, now) // 90% reaches the threshold

	created, err := Sweep(db, now)
	if err != nil || created != 1 {
		t.Fatalf("sweep created=%d err=%v, want 1", created, err)
	}
	act, err := ActiveAction(db, u.ID)
	if err != nil || act == nil {
		t.Fatalf("active action missing: %v", err)
	}
	if act.Trigger != LinkTriggerTraffic || act.UsedBytes != 900 || act.QuotaBytes != 1000 || act.MaxPriceCents != 500 {
		t.Fatalf("action snapshot = %+v", act)
	}
	// Trail carries the trigger model text
	if act.Reason == "" {
		t.Fatal("reason should be recorded")
	}
	// Only one in-app message is sent
	var notes []storage.Notification
	if err := db.Where("user_id = ?", u.ID).Find(&notes).Error; err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Type != storage.NotifSystem {
		t.Fatalf("notifications = %+v", notes)
	}

	// Second round neither re-creates nor re-notifies
	if created, err := Sweep(db, now.Add(time.Minute)); err != nil || created != 0 {
		t.Fatalf("second sweep created=%d err=%v, want 0", created, err)
	}
	var n int64
	db.Model(&storage.Notification{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 1 {
		t.Fatalf("notification dup: %d", n)
	}

	// Usage recedes → release the trail
	if err := db.Where("user_id = ?", u.ID).Delete(&storage.TrafficLog{}).Error; err != nil {
		t.Fatal(err)
	}
	if created, err := Sweep(db, now.Add(2*time.Minute)); err != nil || created != 0 {
		t.Fatalf("release sweep created=%d err=%v", created, err)
	}
	act, err = ActiveAction(db, u.ID)
	if err != nil || act != nil {
		t.Fatalf("action should be released: %v %+v", err, act)
	}
	var released storage.QuotaAction
	if err := db.First(&released).Error; err != nil || released.ReleasedAt == nil || released.ReleaseReason != "阈值回落" {
		t.Fatalf("release trail = %+v err=%v", released, err)
	}
	// Re-trigger after release: new row, new notification
	seedTraffic(t, db, u.ID, nil, 950, 0, now.Add(3*time.Minute))
	if created, err := Sweep(db, now.Add(3*time.Minute)); err != nil || created != 1 {
		t.Fatalf("re-trigger created=%d err=%v", created, err)
	}
}

func TestLinkSweepCostTierAndDisable(t *testing.T) {
	db := newLinkDB(t)
	// Unlimited user goes through the cost tier
	u := seedLinkUser(t, db, "unlimited", 0)
	setting := LinkSetting{Enabled: true, CostCents: 1000, MaxPriceCents: 0}
	if err := SaveLinkSetting(db, setting); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	seedTraffic(t, db, u.ID, nil, 1<<30, 0, now) // 1GB with no node attached: unit price 0, cost 0, no trigger

	if created, err := Sweep(db, now); err != nil || created != 0 {
		t.Fatalf("no-price traffic should not trigger: created=%d err=%v", created, err)
	}

	// Priced traffic reaches the threshold → cost tier triggers
	expensive := storage.Node{Name: "exp", Token: "tok-exp", BillingType: "按流量", TrafficPriceCents: 2000}
	if err := db.Create(&expensive).Error; err != nil {
		t.Fatal(err)
	}
	seedTraffic(t, db, u.ID, &expensive.ID, 1<<30, 0, now) // 1GB × 2000 = 2000 cents ≥ 1000
	if created, err := Sweep(db, now); err != nil || created != 1 {
		t.Fatalf("cost tier should trigger: created=%d err=%v", created, err)
	}
	act, err := ActiveAction(db, u.ID)
	if err != nil || act == nil || act.Trigger != LinkTriggerCost {
		t.Fatalf("cost action = %+v err=%v", act, err)
	}
	// Cost tier snapshot
	if act.CostCents != 2000 {
		t.Fatalf("cost snapshot = %d, want 2000", act.CostCents)
	}
	// Turning off the linkage releases
	if err := SaveLinkSetting(db, LinkSetting{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if created, err := Sweep(db, now.Add(time.Minute)); err != nil || created != 0 {
		t.Fatalf("disable sweep: created=%d err=%v", created, err)
	}
	if act, _ := ActiveAction(db, u.ID); act != nil {
		t.Fatalf("disable should release all, got %+v", act)
	}
	// Disabled users are skipped by linkage evaluation (quota overrun would trigger, but disabled skips)
	u2 := seedLinkUser(t, db, "ghost", 100)
	if err := db.Model(&storage.User{}).Where("id = ?", u2.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	setting.Enabled = true
	setting.CostCents = 0
	setting.TrafficPercent = 90
	if err := SaveLinkSetting(db, setting); err != nil {
		t.Fatal(err)
	}
	seedTraffic(t, db, u2.ID, nil, 1000, 0, now)
	if created, err := Sweep(db, now); err != nil || created != 0 {
		t.Fatalf("disabled user should be skipped: created=%d err=%v", created, err)
	}
	if act, _ := ActiveAction(db, u2.ID); act != nil {
		t.Fatal("disabled user should have no action")
	}
}

// TestLinkLoopSweepsAndStops LinkLoop start/stop: runs one round on start (over-quota user gets a downgrade
// trail), returns after cancel.
func TestLinkLoopSweepsAndStops(t *testing.T) {
	db := newLinkDB(t)
	u := seedLinkUser(t, db, "loop-u", 1000)
	seedTraffic(t, db, u.ID, nil, 900, 0, time.Now()) // 90% triggers
	if err := SaveLinkSetting(db, LinkSetting{Enabled: true, TrafficPercent: 90}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		LinkLoop(ctx, db, 20*time.Millisecond, log.New(io.Discard, "", 0))
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var n int64
		db.Model(&storage.QuotaAction{}).Where("user_id = ?", u.ID).Count(&n)
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("loop never downgraded user")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not stop")
	}
}

// TestHumanMB byte-magnitude human-readable model: B/MB/GB tiers, rounded up.
func TestHumanMB(t *testing.T) {
	for b, want := range map[int64]string{
		512: "512B", 1 << 20: "1MB", (1 << 20) + 1: "2MB",
		3 << 30: "3GB", (3 << 30) + 1: "4GB",
	} {
		if got := humanMB(b); got != want {
			t.Fatalf("humanMB(%d) = %q, want %q", b, got, want)
		}
	}
}
