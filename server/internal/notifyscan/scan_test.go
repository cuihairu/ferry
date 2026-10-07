package notifyscan

import (
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTestDB 建内存库并迁移全部表。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
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

// seedUser 建用户并显式回写布尔偏好：GORM 对零值 bool 的 Create 跳过落
// 默认值 true，且会用默认值回填 struct（故更新 map 必须在 Create 前取值）。
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

// TestSweepExpiry 覆盖到期扫描：窗口内提醒、窗口外不提、关偏好不提、
// 禁用不提、同日去重、跨日再发。
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

	// 同日再扫：去重不发
	if err := Sweep(db, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("type = ?", storage.NotifExpiry).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("same-day sweep should dedup, got %d", len(rows))
	}

	// 跨日再扫：再发一条
	if err := Sweep(db, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("type = ?", storage.NotifExpiry).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("next-day sweep should notify again, got %d", len(rows))
	}

	// 已到期（差值为负）也提醒，文案带「已到期」
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

// TestSweepTraffic 覆盖流量扫描：达阈值提醒、未达不提、关偏好不提、
// 个人阈值生效、同日去重。
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
	// 用量：tr-90 90%（达默认 80）、tr-low 10%（不达）、tr-custom 90%（未达其 95）
	usage := map[string]int64{"tr-90": gb * 90 / 100, "tr-low": gb * 10 / 100, "tr-custom": gb * 90 / 100}
	now := time.Now()
	for name, used := range usage {
		var u storage.User
		if err := db.Where("username = ?", name).First(&u).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&storage.TrafficLog{UserID: u.ID, RxBytes: used, TxBytes: 0, RecordedAt: now}).Error; err != nil {
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

	// 同日去重
	if err := Sweep(db, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("type = ?", storage.NotifTraffic).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("same-day sweep should dedup, got %d", len(rows))
	}
}

// TestSweepEmpty 库里没有候选用户时扫描应安静通过。
func TestSweepEmpty(t *testing.T) {
	db := newTestDB(t)
	if err := Sweep(db, time.Now()); err != nil {
		t.Fatalf("sweep on empty db: %v", err)
	}
}
