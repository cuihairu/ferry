package notifyscan

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTestDB opens an in-memory DB and migrates every table. Isolated by test name + timestamp:
// a shared cache DB would leak rows across tests (the -count=2 cross-talk lesson is on record).
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:notifyscan-%s-%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func at(days int) *time.Time {
	d := time.Now().AddDate(0, 0, days)
	return &d
}

// seedUser creates a user and writes back the boolean preferences explicitly: GORM skips
// the default true on Create for a zero bool and back-fills the struct with the default (so the update map must be taken before Create).
func seedUser(t *testing.T, db *gorm.DB, u storage.User) storage.User {
	t.Helper()
	want := map[string]any{
		"enabled": u.Enabled, "notify_expiry": u.NotifyExpiry, "notify_traffic": u.NotifyTraffic,
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&u).Updates(want).Error; err != nil {
		t.Fatal(err)
	}
	return u
}

// TestSweepExpiry covers the expiry sweep: inside the window reminds, outside does not,
// preference off does not, disabled does not, same-day dedup, next-day re-sends.
func TestSweepExpiry(t *testing.T) {
	db := newTestDB(t)
	users := []storage.User{
		{Username: "exp-3d", SubToken: "t1", QuotaBytes: 1 << 30, ExpiresAt: at(3), Enabled: true, NotifyExpiry: true, TrafficWarnPercent: 80},
		{Username: "exp-20d", SubToken: "t2", QuotaBytes: 1 << 30, ExpiresAt: at(20), Enabled: true, NotifyExpiry: true, TrafficWarnPercent: 80},
		{Username: "exp-off", SubToken: "t3", QuotaBytes: 1 << 30, ExpiresAt: at(2), Enabled: true, NotifyExpiry: false, TrafficWarnPercent: 80},
		{Username: "exp-disabled", SubToken: "t4", QuotaBytes: 1 << 30, ExpiresAt: at(1), Enabled: false, NotifyExpiry: true, TrafficWarnPercent: 80},
	}
	for i := range users {
		users[i] = seedUser(t, db, users[i])
	}
	now := time.Now()
	if err := Sweep(db, now); err != nil {
		t.Fatal(err)
	}
	var rows []storage.Notification
	if err := db.Where("type = ?", storage.NotifExpiry).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expiry notifications = %d, want 1（仅窗口内启用且开偏好的 exp-3d）", len(rows))
	}
	// Out-of-site event (HERALD-4): lands an expire event alongside the in-app notification, target=user:<id>
	var evs []storage.Event
	if err := db.Where("kind = ?", "expire").Find(&evs).Error; err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Target != fmt.Sprintf("user:%d", users[0].ID) ||
		evs[0].DedupKey != fmt.Sprintf("expire:%d:%s", users[0].ID, now.Format("20060102")) {
		t.Fatalf("expire events = %+v", evs)
	}

	// Sweep again the same day: deduped, nothing sent
	if err := Sweep(db, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("type = ?", storage.NotifExpiry).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("same-day sweep should dedup, got %d", len(rows))
	}
	var n int64
	db.Model(&storage.Event{}).Where("kind = ?", "expire").Count(&n)
	if n != 1 {
		t.Fatalf("same-day expire events = %d, want 1", n)
	}

	// Sweep the next day: sends one more
	if err := Sweep(db, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("type = ?", storage.NotifExpiry).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("next-day sweep should notify again, got %d", len(rows))
	}
	db.Model(&storage.Event{}).Where("kind = ?", "expire").Count(&n)
	if n != 2 {
		t.Fatalf("next-day expire events = %d, want 2", n)
	}

	// Already expired (negative difference) also reminds, with "already expired" wording
	past := now.AddDate(0, 0, -2)
	users[0].ExpiresAt = &past
	if err := db.Save(&users[0]).Error; err != nil {
		t.Fatal(err)
	}
	if err := Sweep(db, now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var latest storage.Notification
	if err := db.Where("type = ?", storage.NotifExpiry).Order("id DESC").First(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(latest.Title, "您的账号已到期") {
		t.Fatalf("expired title = %q, want 已到期文案", latest.Title)
	}
}

// TestSweepTraffic covers the traffic sweep: reaching the threshold reminds, below it does not,
// preference off does not, the personal threshold applies, same-day dedup.
func TestSweepTraffic(t *testing.T) {
	db := newTestDB(t)
	gb := int64(1) << 30
	users := []storage.User{
		{Username: "tr-90", SubToken: "q1", QuotaBytes: gb, ResetCycle: "none", Enabled: true, NotifyTraffic: true, TrafficWarnPercent: 80},
		{Username: "tr-low", SubToken: "q2", QuotaBytes: gb, ResetCycle: "none", Enabled: true, NotifyTraffic: true, TrafficWarnPercent: 80},
		{Username: "tr-off", SubToken: "q3", QuotaBytes: gb, ResetCycle: "none", Enabled: true, NotifyTraffic: false, TrafficWarnPercent: 80},
		{Username: "tr-custom", SubToken: "q4", QuotaBytes: gb, ResetCycle: "none", Enabled: true, NotifyTraffic: true, TrafficWarnPercent: 95},
	}
	for i := range users {
		users[i] = seedUser(t, db, users[i])
	}
	// Usage: tr-90 90% (reaches the default 80), tr-low 10% (does not), tr-custom 90% (below its 95)
	usage := map[string]int64{"tr-90": gb * 90 / 100, "tr-low": gb * 10 / 100, "tr-custom": gb * 90 / 100}
	now := time.Now()
	for name, used := range usage {
		var u storage.User
		if err := db.Where("username = ?", name).First(&u).Error; err != nil {
			t.Fatal(err)
		}
		uid := u.ID
		if err := db.Create(&storage.TrafficLog{UserID: &uid, RxBytes: used, TxBytes: 0, RecordedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := Sweep(db, now); err != nil {
		t.Fatal(err)
	}
	var rows []storage.Notification
	if err := db.Where("type = ?", storage.NotifTraffic).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("traffic notifications = %d, want 1（仅 tr-90 达阈值）", len(rows))
	}
	// Out-of-site event (HERALD-4): lands a quota event alongside the in-app notification
	var tr storage.User
	if err := db.Where("username = ?", "tr-90").First(&tr).Error; err != nil {
		t.Fatal(err)
	}
	var evs []storage.Event
	if err := db.Where("kind = ?", "quota").Find(&evs).Error; err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Target != fmt.Sprintf("user:%d", tr.ID) {
		t.Fatalf("quota events = %+v", evs)
	}

	// Same-day dedup
	if err := Sweep(db, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("type = ?", storage.NotifTraffic).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("same-day sweep should dedup, got %d", len(rows))
	}
	var n int64
	db.Model(&storage.Event{}).Where("kind = ?", "quota").Count(&n)
	if n != 1 {
		t.Fatalf("same-day quota events = %d, want 1", n)
	}
}

// TestSweepEmpty: with no candidate users in the DB the sweep should pass quietly.
func TestSweepEmpty(t *testing.T) {
	db := newTestDB(t)
	if err := Sweep(db, time.Now()); err != nil {
		t.Fatalf("sweep on empty db: %v", err)
	}
}

// TestLoopSweepsAndStops covers the periodic loop: the sweep at the tick writes the expiry reminder to the DB, and it returns on cancel.
func TestLoopSweepsAndStops(t *testing.T) {
	db := newTestDB(t)
	u := seedUser(t, db, storage.User{Username: "loop-exp", SubToken: "tl", QuotaBytes: 1 << 30,
		ExpiresAt: at(3), Enabled: true, NotifyExpiry: true, TrafficWarnPercent: 80})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		Loop(ctx, db, 20*time.Millisecond, nil)
	}()

	var n int64
	deadline := time.Now().Add(5 * time.Second)
	for {
		db.Model(&storage.Notification{}).
			Where("user_id = ? AND type = ?", u.ID, storage.NotifExpiry).Count(&n)
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("loop sweep never notified; count=%d", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop after cancel")
	}
}

// TestSweepTrafficAnomaly covers the traffic anomaly sweep (F-1252): a today-vs-
// trailing-baseline spike fires exactly one admin event, deduped per day; a
// below-ratio user and a no-baseline (floor-guarded) user stay silent.
func TestSweepTrafficAnomaly(t *testing.T) {
	db := newTestDB(t)
	mib := int64(1) << 20
	users := []storage.User{
		{Username: "an-spike", SubToken: "a1", QuotaBytes: 1 << 30, ResetCycle: "none", Enabled: true, TrafficWarnPercent: 80},
		{Username: "an-quiet", SubToken: "a2", QuotaBytes: 1 << 30, ResetCycle: "none", Enabled: true, TrafficWarnPercent: 80},
		{Username: "an-fresh", SubToken: "a3", QuotaBytes: 1 << 30, ResetCycle: "none", Enabled: true, TrafficWarnPercent: 80},
	}
	for i := range users {
		users[i] = seedUser(t, db, users[i])
	}
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	seed := func(name string, day int, rx int64) {
		t.Helper()
		var u storage.User
		if err := db.Where("username = ?", name).First(&u).Error; err != nil {
			t.Fatal(err)
		}
		uid := u.ID
		if err := db.Create(&storage.TrafficLog{UserID: &uid, RxBytes: rx, TxBytes: 0,
			RecordedAt: dayStart.AddDate(0, 0, day).Add(time.Hour)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Baseline 16 MiB/day for spike and quiet; today 96 MiB (= 6x, fires) vs 20 MiB (stays).
	for d := -7; d <= -1; d++ {
		seed("an-spike", d, 16*mib)
		seed("an-quiet", d, 16*mib)
	}
	seed("an-spike", 0, 96*mib)
	seed("an-quiet", 0, 20*mib)
	// Fresh user: a big today with no history must not fire (baseline floor).
	seed("an-fresh", 0, 96*mib)

	if err := Sweep(db, now); err != nil {
		t.Fatal(err)
	}
	var evs []storage.Event
	if err := db.Where("kind = ?", herald.KindTrafficAnomaly).Find(&evs).Error; err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("anomaly events = %d, want 1 (only an-spike fires): %+v", len(evs), evs)
	}
	var spike storage.User
	if err := db.Where("username = ?", "an-spike").First(&spike).Error; err != nil {
		t.Fatal(err)
	}
	if evs[0].Target != herald.TargetAdmin || evs[0].Severity != herald.SeverityWarning {
		t.Fatalf("event head: %+v", evs[0])
	}
	if evs[0].DedupKey != fmt.Sprintf("traffic_anomaly:%d:%s", spike.ID, now.Format("2006-01-02")) {
		t.Fatalf("dedup key: %s", evs[0].DedupKey)
	}

	// Same-day dedup: a second sweep adds nothing.
	if err := Sweep(db, now); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&storage.Event{}).Where("kind = ?", herald.KindTrafficAnomaly).Count(&n)
	if n != 1 {
		t.Fatalf("same-day sweep should dedup, got %d events", n)
	}
}

// TestSweepNodeQuota covers F-1825: a node whose current-month traffic hits
// its monthly_traffic_quota_bytes lands one node_quota admin warning (deduped
// per node per month); a node below its limit and an unlimited (quota=0) node
// stay quiet, and last month's rows don't count toward this month.
func TestSweepNodeQuota(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	over := storage.Node{Name: "nq-over", Token: "nq-t1", MonthlyTrafficQuotaBytes: 1 << 30}
	below := storage.Node{Name: "nq-below", Token: "nq-t2", MonthlyTrafficQuotaBytes: 1 << 30}
	free := storage.Node{Name: "nq-free", Token: "nq-t3"}
	for _, nd := range []*storage.Node{&over, &below, &free} {
		if err := db.Create(nd).Error; err != nil {
			t.Fatal(err)
		}
	}
	lastMonth := now.AddDate(0, -1, 0)
	logs := []storage.NodeTrafficLog{
		{NodeID: over.ID, Proc: "xray", TxBytes: 2 << 30, RecordedAt: now},
		{NodeID: over.ID, Proc: "xray", TxBytes: 3 << 30, RecordedAt: lastMonth},
		{NodeID: below.ID, Proc: "xray", RxBytes: 1 << 29, RecordedAt: now},
		{NodeID: free.ID, Proc: "xray", TxBytes: 5 << 30, RecordedAt: now},
	}
	for i := range logs {
		if err := db.Create(&logs[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := Sweep(db, now); err != nil {
		t.Fatal(err)
	}
	var evs []storage.Event
	if err := db.Where("kind = ?", herald.KindNodeQuota).Order("id").Find(&evs).Error; err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("node_quota events = %d, want 1 (only nq-over)", len(evs))
	}
	if evs[0].Target != herald.TargetAdmin || evs[0].Severity != herald.SeverityWarning {
		t.Fatalf("event head: %+v", evs[0])
	}
	if evs[0].DedupKey != fmt.Sprintf("node_quota:%d:%s", over.ID, now.Format("200601")) {
		t.Fatalf("dedup key: %s", evs[0].DedupKey)
	}

	// Same-month dedup: a second sweep adds nothing.
	if err := Sweep(db, now); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&storage.Event{}).Where("kind = ?", herald.KindNodeQuota).Count(&n)
	if n != 1 {
		t.Fatalf("same-month sweep should dedup, got %d events", n)
	}
}
