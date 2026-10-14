package announce

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
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

func mkUser(db *gorm.DB, id uint, enabled bool, expOffset *time.Duration) storage.User {
	u := storage.User{
		ID:       id,
		Username: "u" + string(rune('a'+id)),
		SubToken: "tk" + string(rune('a'+id)),
		Enabled:  true,
	}
	if expOffset != nil {
		ts := time.Now().Add(*expOffset)
		u.ExpiresAt = &ts
	}
	if err := db.Create(&u).Error; err != nil {
		panic(err)
	}
	// GORM default 标签吞 Create 零值（TOUCH-1 注记），停用走 Update 显式落库
	if !enabled {
		if err := db.Model(&storage.User{}).Where("id = ?", u.ID).Update("enabled", false).Error; err != nil {
			panic(err)
		}
	}
	return u
}

func expIn(days int) *time.Duration {
	d := time.Duration(days) * 24 * time.Hour
	return &d
}

func notifRows(db *gorm.DB, cond string, args ...interface{}) (int, error) {
	var n int64
	if err := db.Model(&storage.Notification{}).Where(cond, args...).Count(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}

func eventRows(db *gorm.DB, cond string, args ...interface{}) (int, error) {
	var n int64
	if err := db.Model(&storage.Event{}).Where(cond, args...).Count(&n).Error; err != nil {
		return 0, err
	}
	return int(n), nil
}

func getAnchor(db *gorm.DB, id int64) (storage.Notification, error) {
	var a storage.Notification
	if err := db.Where("id = ?", id).First(&a).Error; err != nil {
		return a, err
	}
	return a, nil
}

func TestNormalizeAudience(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  error
	}{
		{"", "all", nil},
		{"all", "all", nil},
		{"expiring:7", "expiring:7", nil},
		{"expiring:07", "expiring:7", nil},
		{"expiring:1", "expiring:1", nil},
		{"expiring:90", "expiring:90", nil},
		{"expiring:0", "", ErrBadAudience},
		{"expiring:91", "", ErrBadAudience},
		{"expiring:-1", "", ErrBadAudience},
		{"expiring:abc", "", ErrBadAudience},
		{"expiring:", "", ErrBadAudience},
		{"foo", "", ErrBadAudience},
		{"EXPIRING:7", "", ErrBadAudience},
	}
	for _, c := range cases {
		got, err := NormalizeAudience(c.in)
		if got != c.want || !errors.Is(err, c.err) {
			t.Errorf("NormalizeAudience(%q) = %q, %v; want %q, %v", c.in, got, err, c.want, c.err)
		}
	}
}

func TestResendDedupKey(t *testing.T) {
	now := time.Date(2026, 10, 13, 14, 32, 0, 0, time.UTC)
	got := resendDedupKey(42, now)
	want := "notice_resend:42:202610131432"
	if got != want {
		t.Errorf("resendDedupKey(42, %v) = %q; want %q", now, got, want)
	}
}

func TestFanoutImmediate(t *testing.T) {
	db := testDB(t)
	mkUser(db, 1, true, nil)
	mkUser(db, 2, true, nil)
	mkUser(db, 3, false, nil)

	cnt, err := Fanout(db, Input{Title: "t", Body: "b", Audience: ""})
	if err != nil {
		t.Fatalf("Fanout = %v", err)
	}
	if cnt != 2 {
		t.Fatalf("fanout count = %d; want 2 (disabled user excluded)", cnt)
	}
	anchors, _ := notifRows(db, "user_id = ? AND type = ?", 0, storage.NotifAnnouncement)
	userRows, err := notifRows(db, "type = ? AND user_id > ?", storage.NotifAnnouncement, 0)
	if err != nil {
		t.Fatalf("count user rows: %v", err)
	}
	if anchors != 1 || userRows != 2 {
		t.Fatalf("anchor rows = %d, user rows = %d; want 1, 2", anchors, userRows)
	}
	var anchor storage.Notification
	if err := db.Where("user_id = ? AND type = ?", 0, storage.NotifAnnouncement).First(&anchor).Error; err != nil {
		t.Fatalf("get anchor: %v", err)
	}
	if anchor.Audience != "all" {
		t.Errorf("anchor audience = %q; want all", anchor.Audience)
	}
	if anchor.FanoutAt == nil {
		t.Error("immediate fanout should set fanout_at")
	}
	n, err := eventRows(db, "kind = ? AND status = ?", herald.KindNotice, herald.StatusPending)
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 2 {
		t.Errorf("notice events = %d; want 2", n)
	}
}

func TestFanoutScheduled(t *testing.T) {
	db := testDB(t)
	mkUser(db, 1, true, nil)

	start := time.Now().Add(time.Hour)
	cnt, err := Fanout(db, Input{Title: "t", StartsAt: &start, Audience: "all"})
	if err != nil {
		t.Fatalf("Fanout = %v", err)
	}
	if cnt != 0 {
		t.Fatalf("scheduled fanout count = %d; want 0", cnt)
	}
	var anchor storage.Notification
	if err := db.Where("user_id = 0 AND type = ?", storage.NotifAnnouncement).First(&anchor).Error; err != nil {
		t.Fatalf("get anchor: %v", err)
	}
	if anchor.FanoutAt != nil {
		t.Error("scheduled anchor should keep fanout_at empty")
	}
	if anchor.Audience != "all" {
		t.Errorf("anchor audience = %q; want all", anchor.Audience)
	}
	userRows, _ := notifRows(db, "user_id > ?", 0)
	if userRows != 0 {
		t.Errorf("scheduled should create no user rows; got %d", userRows)
	}
	if n, _ := eventRows(db, "kind = ?", herald.KindNotice); n != 0 {
		t.Errorf("scheduled should emit no events; got %d", n)
	}
}

func TestFanoutExpiring(t *testing.T) {
	db := testDB(t)
	mkUser(db, 1, true, expIn(3))  // 3 天内到期：命中
	mkUser(db, 2, true, expIn(30)) // 超出窗口
	mkUser(db, 3, true, nil)       // 未设到期
	mkUser(db, 4, false, expIn(3)) // 已停用
	expired := time.Now().Add(-24 * time.Hour)

	cnt, err := Fanout(db, Input{Title: "t", Audience: "expiring:7"})
	if err != nil {
		t.Fatalf("Fanout = %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expiring:7 fanout count = %d; want 1", cnt)
	}
	var rows []storage.Notification
	if err := db.Where("type = ? AND user_id > ?", storage.NotifAnnouncement, 0).
		Order("user_id").Find(&rows).Error; err != nil {
		t.Fatalf("list user rows: %v", err)
	}
	if len(rows) != 1 || rows[0].UserID != 1 {
		t.Fatalf("expiring rows = %+v; want only user 1", rows)
	}
	if err := db.Create(&storage.User{ID: 5, Username: "u5", SubToken: "tk5",
		Enabled: true, ExpiresAt: &expired}).Error; err != nil {
		t.Fatalf("create expired user: %v", err)
	}
	// 已过期（expires_at <= now）不属 expiring 名单
	cnt2, err := Fanout(db, Input{Title: "t2", Audience: "expiring:7"})
	if err != nil {
		t.Fatalf("Fanout2 = %v", err)
	}
	if cnt2 != 1 {
		t.Errorf("second expiring fanout = %d; want 1 (expired user excluded)", cnt2)
	}
}

func TestFanoutBadAudience(t *testing.T) {
	db := testDB(t)
	mkUser(db, 1, true, nil)
	_, err := Fanout(db, Input{Title: "t", Audience: "expiring:999"})
	if !errors.Is(err, ErrBadAudience) {
		t.Fatalf("err = %v; want ErrBadAudience", err)
	}
}

func TestRunDue(t *testing.T) {
	db := testDB(t)
	mkUser(db, 1, true, nil)
	mkUser(db, 2, true, nil)

	start := time.Now().Add(-time.Hour)
	anchor := storage.Notification{UserID: 0, Type: storage.NotifAnnouncement, Title: "t",
		Audience: "all", StartsAt: &start, CreatedAt: time.Now()}
	if err := db.Create(&anchor).Error; err != nil {
		t.Fatalf("create anchor: %v", err)
	}

	n, err := RunDue(db, time.Now())
	if err != nil {
		t.Fatalf("RunDue = %v", err)
	}
	if n != 2 {
		t.Fatalf("RunDue = %d; want 2", n)
	}
	a, err := getAnchor(db, anchor.ID)
	if err != nil {
		t.Fatalf("get anchor: %v", err)
	}
	if a.FanoutAt == nil {
		t.Error("RunDue should set fanout_at")
	}
	userRows, _ := notifRows(db, "user_id > ?", 0)
	if userRows != 2 {
		t.Errorf("user rows = %d; want 2", userRows)
	}

	// 已扇出锚点不重复扇出
	n2, err := RunDue(db, time.Now())
	if err != nil {
		t.Fatalf("RunDue2 = %v", err)
	}
	if n2 != 0 {
		t.Errorf("second RunDue = %d; want 0 (already fanned out)", n2)
	}
}

func TestResend(t *testing.T) {
	db := testDB(t)
	mkUser(db, 1, true, nil)
	mkUser(db, 2, true, nil)
	anchor := storage.Notification{UserID: 0, Type: storage.NotifAnnouncement, Title: "t",
		Audience: "all", CreatedAt: time.Now()}
	if err := db.Create(&anchor).Error; err != nil {
		t.Fatalf("create anchor: %v", err)
	}

	now := time.Now()
	cnt, err := Resend(db, anchor.ID, now)
	if err != nil {
		t.Fatalf("Resend = %v", err)
	}
	if cnt != 2 {
		t.Fatalf("Resend = %d; want 2", cnt)
	}
	a, err := getAnchor(db, anchor.ID)
	if err != nil {
		t.Fatalf("get anchor: %v", err)
	}
	if a.ResendAt == nil {
		t.Fatal("resend_at should be set")
	}
	if n, _ := eventRows(db, "kind = ?", herald.KindNotice); n != 2 {
		t.Errorf("notice events = %d; want 2", n)
	}
	var ev storage.Event
	if err := db.Where("kind = ?", herald.KindNotice).First(&ev).Error; err != nil {
		t.Fatalf("get event: %v", err)
	}
	if ev.DedupKey != resendDedupKey(anchor.ID, now) {
		t.Errorf("dedup key = %q; want %q", ev.DedupKey, resendDedupKey(anchor.ID, now))
	}

	// 限频窗口内拒绝
	if _, err := Resend(db, anchor.ID, now.Add(time.Minute)); !errors.Is(err, ErrCooldown) {
		t.Fatalf("second Resend err = %v; want ErrCooldown", err)
	}

	// 超过窗口可再发
	cnt2, err := Resend(db, anchor.ID, now.Add(7*time.Hour))
	if err != nil {
		t.Fatalf("Resend after cooldown = %v", err)
	}
	if cnt2 != 2 {
		t.Errorf("resend after cooldown = %d; want 2", cnt2)
	}
}

func TestResendWindowClosed(t *testing.T) {
	db := testDB(t)
	past := time.Now().Add(-time.Hour)
	anchor := storage.Notification{UserID: 0, Type: storage.NotifAnnouncement, Title: "t",
		EndsAt: &past, CreatedAt: time.Now()}
	if err := db.Create(&anchor).Error; err != nil {
		t.Fatalf("create anchor: %v", err)
	}
	if _, err := Resend(db, anchor.ID, time.Now()); !errors.Is(err, ErrWindowClosed) {
		t.Fatalf("err = %v; want ErrWindowClosed", err)
	}
}

func TestResendNotFound(t *testing.T) {
	db := testDB(t)
	if _, err := Resend(db, 999, time.Now()); err == nil {
		t.Fatal("resend of unknown anchor should error")
	}
}

func TestAudienceScope(t *testing.T) {
	db := testDB(t)
	mkUser(db, 1, true, nil)
	mkUser(db, 2, false, nil)
	mkUser(db, 3, true, expIn(3))
	now := time.Now()

	pluck := func(aud string) []int64 {
		var ids []int64
		if err := audienceScope(db, &storage.Notification{Audience: aud}, now).
			Order("id").Pluck("id", &ids).Error; err != nil {
			t.Fatalf("audienceScope(%q) = %v", aud, err)
		}
		return ids
	}

	cases := []struct {
		aud  string
		want []int64
	}{
		{"", []int64{1, 3}},           // 缺省=全员启用
		{"all", []int64{1, 3}},        // 全员
		{"expiring:7", []int64{3}},    // 命中窗口
		{"expiring:90", []int64{3}},   // 大窗口同名单
		{"expiring:0", []int64{1, 3}}, // 越界=非法，兜底全员
		{"garbage", []int64{1, 3}},    // 脏数据兜底全员不阻塞
		{"EXP", []int64{1, 3}},
	}
	for _, c := range cases {
		got := pluck(c.aud)
		if len(got) != len(c.want) {
			t.Errorf("audienceScope(%q) = %v; want %v", c.aud, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("audienceScope(%q) = %v; want %v", c.aud, got, c.want)
				break
			}
		}
	}
}
