package toucher

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&storage.User{}, &storage.UserContact{}, &storage.EntryDomain{},
		&storage.TouchJob{}, &storage.Event{}, &storage.Setting{}, &storage.TrafficLog{}, &storage.SaveStat{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

var cfg = Config{DomainsDays: 7, BaseURL: "https://panel.example.com"}

// TestSweepBill covers monthly bills (TOUCH-4): one job per enabled user, always sent (no preference filter),
// no resend within the same month by the month gate, jobs marked sent with event_id, highlights wired to UserSavings.
func TestSweepBill(t *testing.T) {
	db := newTestDB(t)
	users := []storage.User{
		{Username: "bill-a", SubToken: "ta", Enabled: true, QuotaBytes: 10_000_000_000},
		{Username: "bill-b", SubToken: "tb", Enabled: false},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	// gorm default:true swallows the zero value: patch the disabled state explicitly after create.
	if err := db.Model(&storage.User{}).Where("username = ?", "bill-b").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// A's month usage 2GB + savings counters (node 1 holds all of it: direct 1GB, blocked 0.5GB)
	nid := uint(1)
	uid := users[0].ID
	if err := db.Create(&[]storage.TrafficLog{
		{UserID: &uid, NodeID: &nid, RxBytes: 2_000_000_000, RecordedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.SaveStat{NodeID: 1, Day: now.UTC().Format("2006-01-02"),
		DirectBytes: 1_000_000_000, BlockedBytes: 500_000_000}).Error; err != nil {
		t.Fatal(err)
	}

	if err := Sweep(db, cfg, now); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var jobs []storage.TouchJob
	if err := db.Where("kind = ?", "bill").Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 { // disabled users get none
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if jobs[0].Status != "sent" || jobs[0].EventID == 0 || jobs[0].SentAt == nil {
		t.Fatalf("job not sent: %+v", jobs[0])
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(jobs[0].Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["title"] != "月账单 · "+now.Format("2006-01") {
		t.Fatalf("title: %q", payload["title"])
	}
	for _, want := range []string{"2.00 GB", "10.00 GB", "1.00 GB", "500.0 MB", cfg.BaseURL + "/panel"} {
		if !contains(payload["body"], want) {
			t.Fatalf("body missing %q:\n%s", want, payload["body"])
		}
	}
	// The corresponding outbox event carries the user target and the touch_job_id link
	var ev storage.Event
	if err := db.Where("kind = ? AND id = ?", "bill", jobs[0].EventID).First(&ev).Error; err != nil {
		t.Fatalf("event: %v", err)
	}
	if ev.Target != herald.TargetUser(int64(users[0].ID)) {
		t.Fatalf("target: %s", ev.Target)
	}

	// A second round in the same month does not resend
	if err := Sweep(db, cfg, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&storage.TouchJob{}).Where("kind = ?", "bill").Count(&n)
	if n != 1 {
		t.Fatalf("bill re-sent in same month: %d", n)
	}
}

// TestSweepDomains covers the domains routine (TOUCH-4): only users with a bound email who have not
// unsubscribed, primary/backup grouping, the period gate, and 0 = off.
func TestSweepDomains(t *testing.T) {
	db := newTestDB(t)
	users := []storage.User{
		{Username: "mail-a", SubToken: "da", Enabled: true},
		{Username: "mail-b", SubToken: "db", Enabled: true},
		{Username: "mail-c", SubToken: "dc", Enabled: true},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	subF := false
	if err := db.Create(&[]storage.UserContact{
		{UserID: users[0].ID, Email: "a@x.c", RoutineEmails: true, BoundAt: time.Now()},
		{UserID: users[1].ID, Email: "b@x.c", RoutineEmails: subF, BoundAt: time.Now()},    // unsubscribed
		{UserID: users[2].ID, TgChatID: "10086", RoutineEmails: true, BoundAt: time.Now()}, // no email
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]storage.EntryDomain{
		{Domain: "p.example.com", Role: "primary", Enabled: true, UpdatedAt: time.Now()},
		{Domain: "b1.example.org", Role: "backup", Enabled: true, UpdatedAt: time.Now()},
		{Domain: "off.example.net", Role: "backup", Enabled: false, UpdatedAt: time.Now()},
	}).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	if err := Sweep(db, cfg, now); err != nil {
		t.Fatal(err)
	}
	var jobs []storage.TouchJob
	if err := db.Where("kind = ?", "domains").Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].UserID != int64(users[0].ID) {
		t.Fatalf("jobs = %+v", jobs)
	}
	var payload map[string]string
	_ = json.Unmarshal([]byte(jobs[0].Payload), &payload)
	for _, want := range []string{"p.example.com", "b1.example.org", "/feed.xml"} {
		if !contains(payload["body"], want) {
			t.Fatalf("body missing %q", want)
		}
	}
	if contains(payload["body"], "off.example.net") {
		t.Fatal("disabled domain leaked")
	}

	// A second round within the period does not resend
	if err := Sweep(db, cfg, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&storage.TouchJob{}).Where("kind = ?", "domains").Count(&n)
	if n != 1 {
		t.Fatalf("domains re-sent within period: %d", n)
	}
	// Period elapsed: resend after rewinding the previous round's time past the gate
	if err := Sweep(db, cfg, now.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	db.Model(&storage.TouchJob{}).Where("kind = ?", "domains").Count(&n)
	if n != 2 {
		t.Fatalf("domains after period: %d", n)
	}

	// 0 = off: a fresh DB yields zero jobs for one round
	db2 := newTestDB(t)
	if err := Sweep(db2, Config{DomainsDays: 0}, now); err != nil {
		t.Fatal(err)
	}
	var n2 int64
	db2.Model(&storage.TouchJob{}).Where("kind = ?", "domains").Count(&n2)
	if n2 != 0 {
		t.Fatalf("disabled domains sent: %d", n2)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// TestLoopSweepsAndStops covers Loop start/stop: interval is injectable, one round runs on start
// (the bill month gate lands a job), and it exits after ctx cancel; nil logger falls back.
func TestLoopSweepsAndStops(t *testing.T) {
	db := newTestDB(t)
	if err := db.Create(&storage.User{Username: "bill-a", SubToken: "ta", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, Config{}, 20*time.Millisecond, nil)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var jobs []storage.TouchJob
		if err := db.Find(&jobs).Error; err != nil {
			t.Fatal(err)
		}
		if len(jobs) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("loop first sweep never landed: %+v", jobs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after cancel")
	}
}

// TestFirstErr covers error merging: the earlier one wins; if the earlier is nil, take the latter.
func TestFirstErr(t *testing.T) {
	a := errors.New("a")
	b := errors.New("b")
	if got := firstErr(a, b); got != a {
		t.Fatalf("firstErr(a,b) = %v; want a", got)
	}
	if got := firstErr(nil, b); got != b {
		t.Fatalf("firstErr(nil,b) = %v; want b", got)
	}
	if got := firstErr(nil, nil); got != nil {
		t.Fatalf("firstErr(nil,nil) = %v; want nil", got)
	}
}

// TestJoinErrAndExpireText covers joinErr's merged two-error criteria (either failing leg surfaces both),
// and expireText's rendering (nil = no expiry; non-nil by the server's local calendar date).
func TestJoinErrAndExpireText(t *testing.T) {
	db := newTestDB(t)
	if sqlDB, err := db.DB(); err == nil {
		// Close the connection pool: both legs of the sweep fail, so joinErr must merge two errors rather than return only the first.
		if err := sqlDB.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := Sweep(db, cfg, time.Now()); err == nil || !strings.Contains(err.Error(), "; ") {
		t.Fatalf("sweep on closed db = %v; want merged errors", err)
	}
	if got := expireText(nil); got != "不限期" {
		t.Fatalf("expireText(nil) = %q", got)
	}
	future := time.Date(2027, 1, 2, 10, 0, 0, 0, time.UTC)
	if got := expireText(&future); got != "2027-01-02" {
		t.Fatalf("expireText = %q", got)
	}
}

// TestBillGateFollowsLocalCalendar covers the P2 "local timezone billing" fix: the bill month gate /
// title follow the server's local calendar month, not the UTC month — sweeping bills at local
// 11-01 02:00 in UTC+8 (= UTC 10-31 18:00), before the fix the UTC month gate recorded October and
// the title carried the UTC month, one month off from the local calendar; after the fix it records
// November and the usage window (quota month criteria) still counts the local November traffic rows.
func TestBillGateFollowsLocalCalendar(t *testing.T) {
	db := newTestDB(t)
	if err := db.Create(&storage.User{Username: "bill-tz", SubToken: "ttz", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	var u storage.User
	if err := db.Where("username = ?", "bill-tz").First(&u).Error; err != nil {
		t.Fatal(err)
	}
	uid := u.ID
	nid := uint(1)
	// UTC+8 test clock; the traffic row lands at local 11-01 01:00 (the calendar month is already open).
	tz := time.FixedZone("billtz", 8*3600)
	now := time.Date(2026, 11, 1, 2, 0, 0, 0, tz)
	if err := db.Create(&storage.TrafficLog{
		UserID: &uid, NodeID: &nid, RxBytes: 800,
		RecordedAt: time.Date(2026, 11, 1, 1, 0, 0, 0, tz),
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := Sweep(db, cfg, now); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var jobs []storage.TouchJob
	if err := db.Where("kind = ?", "bill").Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(jobs[0].Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["title"] != "月账单 · 2026-11" {
		t.Fatalf("title = %q, want local month 2026-11", payload["title"])
	}
	if !contains(payload["body"], "800 B") {
		t.Fatalf("in-month usage missing:\n%s", payload["body"])
	}
	// The month gate key also lands on the local month: rescanning within the month does not resend.
	month, ok, err := storage.GetSetting(db, settingBillMonth)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || month != "2026-11" {
		t.Fatalf("gate setting = %q ok=%v, want 2026-11", month, ok)
	}
	if err := Sweep(db, cfg, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&storage.TouchJob{}).Where("kind = ?", "bill").Count(&n)
	if n != 1 {
		t.Fatalf("bill re-sent in same local month: %d", n)
	}
}

// TestExpireTextLocalCalendar covers codex deep review P2 "unified expiry display timezone":
// the timezone read back from the DB is not fixed (sqlite reads back UTC text), so display must
// convert to the server's local calendar uniformly — UTC 2026-10-31 20:00 is already 11-01 04:00
// in UTC+8, and a bare Format would land on the previous day, one day off from the billing window (local calendar month).
func TestExpireTextLocalCalendar(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("exp-tz", 8*3600)
	defer func() { time.Local = old }()

	if got := expireText(nil); got != "不限期" {
		t.Fatalf("nil = %q", got)
	}
	u := time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)
	if got := expireText(&u); got != "2026-11-01" {
		t.Fatalf("utc 20:00 = %q, want 2026-11-01", got)
	}
	// Before the fix (a bare Format without converting to local) it landed on the previous day; used here as the counter-proof.
	if got := u.Format("2006-01-02"); got != "2026-10-31" {
		t.Fatalf("pre-fix probe = %q, want 2026-10-31", got)
	}
}
