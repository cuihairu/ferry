package toucher

import (
	"encoding/json"
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

// TestSweepBill 月账单（TOUCH-4）：启用用户各一单、必收（无偏好过滤）、
// 月闸门同月不重发、任务 sent 记 event_id、亮点接 UserSavings。
func TestSweepBill(t *testing.T) {
	db := newTestDB(t)
	users := []storage.User{
		{Username: "bill-a", SubToken: "ta", Enabled: true, QuotaBytes: 10_000_000_000},
		{Username: "bill-b", SubToken: "tb", Enabled: false},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	// gorm default:true 吞零值：禁用态建后显式改。
	if err := db.Model(&storage.User{}).Where("username = ?", "bill-b").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// A 本月用量 2GB + 节省计数（节点 1 全占：直连 1GB、拦截 0.5GB）
	nid := uint(1)
	if err := db.Create(&[]storage.TrafficLog{
		{UserID: users[0].ID, NodeID: &nid, RxBytes: 2_000_000_000, RecordedAt: now},
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
	if len(jobs) != 1 { // 禁用用户不收
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if jobs[0].Status != "sent" || jobs[0].EventID == 0 || jobs[0].SentAt == nil {
		t.Fatalf("job not sent: %+v", jobs[0])
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(jobs[0].Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["title"] != "月账单 · "+now.UTC().Format("2006-01") {
		t.Fatalf("title: %q", payload["title"])
	}
	for _, want := range []string{"2.00 GB", "10.00 GB", "1.00 GB", "500.0 MB", cfg.BaseURL + "/panel"} {
		if !contains(payload["body"], want) {
			t.Fatalf("body missing %q:\n%s", want, payload["body"])
		}
	}
	// 对应 outbox 事件带 user target 与 touch_job_id 关联
	var ev storage.Event
	if err := db.Where("kind = ? AND id = ?", "bill", jobs[0].EventID).First(&ev).Error; err != nil {
		t.Fatalf("event: %v", err)
	}
	if ev.Target != herald.TargetUser(int64(users[0].ID)) {
		t.Fatalf("target: %s", ev.Target)
	}

	// 同月二轮不重发
	if err := Sweep(db, cfg, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&storage.TouchJob{}).Where("kind = ?", "bill").Count(&n)
	if n != 1 {
		t.Fatalf("bill re-sent in same month: %d", n)
	}
}

// TestSweepDomains 域名例行（TOUCH-4）：只发绑邮箱且未退订的用户、主备分组、
// 周期闸门、0=关。
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
		{UserID: users[1].ID, Email: "b@x.c", RoutineEmails: subF, BoundAt: time.Now()},    // 退订
		{UserID: users[2].ID, TgChatID: "10086", RoutineEmails: true, BoundAt: time.Now()}, // 无邮箱
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

	// 周期内二轮不重发
	if err := Sweep(db, cfg, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&storage.TouchJob{}).Where("kind = ?", "domains").Count(&n)
	if n != 1 {
		t.Fatalf("domains re-sent within period: %d", n)
	}
	// 周期到期再发：上轮时刻回拨后过闸
	if err := Sweep(db, cfg, now.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	db.Model(&storage.TouchJob{}).Where("kind = ?", "domains").Count(&n)
	if n != 2 {
		t.Fatalf("domains after period: %d", n)
	}

	// 0=关：新库一轮零任务
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
