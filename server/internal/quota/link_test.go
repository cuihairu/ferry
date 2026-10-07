package quota

import (
	"fmt"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newLinkDB(t *testing.T) *gorm.DB {
	t.Helper()
	// 按测试名+时刻隔离内存库：file::memory:?cache=shared 全包共库会跨测试
	// 残留用户/流水造成联动判定串场，-count=2 重跑也会撞旧数据。
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

// seedLinkUser 注意：want map 必须先于 Create 构造——GORM default 标签字段
// Create 后经 RETURNING 回填 struct，之后取到的值不一定是想要的。
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

	// 未设置回缺省：关、流量档 90%
	got, err := LoadLinkSetting(db)
	if err != nil || got.Enabled || got.TrafficPercent != DefaultTrafficPercent {
		t.Fatalf("default setting = %+v err=%v", got, err)
	}

	// 保存回读
	want := LinkSetting{Enabled: true, TrafficPercent: 95, CostCents: 2000, MaxPriceCents: 500}
	if err := SaveLinkSetting(db, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err = LoadLinkSetting(db)
	if err != nil || got != want {
		t.Fatalf("roundtrip = %+v err=%v, want %+v", got, err, want)
	}

	// 启用但两档触发全关：拒绝
	if err := SaveLinkSetting(db, LinkSetting{Enabled: true}); err == nil {
		t.Fatal("enabled with no trigger should fail")
	}
	// 越界归位：percent 300 → 90，负值归 0
	if err := SaveLinkSetting(db, LinkSetting{Enabled: true, TrafficPercent: 300, CostCents: -1, MaxPriceCents: -5}); err != nil {
		t.Fatalf("save out-of-range: %v", err)
	}
	got, _ = LoadLinkSetting(db)
	if got.TrafficPercent != DefaultTrafficPercent || got.CostCents != 0 || got.MaxPriceCents != 0 {
		t.Fatalf("normalize = %+v", got)
	}

	// 脏数据按缺省走
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
	// 配额 0（不限量）不按流量档触发，但费用档可以
	if got := s.Trigger(1<<40, 0, 500); got != "" {
		t.Fatalf("quota=0 should skip traffic tier, got %q", got)
	}
	if got := s.Trigger(1<<40, 0, 1000); got != LinkTriggerCost {
		t.Fatalf("cost tier should trigger, got %q", got)
	}
	// 两档都命中报流量档
	if got := s.Trigger(900, 1000, 2000); got != LinkTriggerTraffic {
		t.Fatalf("traffic tier wins, got %q", got)
	}
	// 关闭全不触发
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
	// 档线 0：只有包月算低成本档
	if (LinkSetting{}).LowCost(cheap) {
		t.Fatal("档线 0 时按流量节点不应算低成本档")
	}
}

func seedTraffic(t *testing.T, db *gorm.DB, userID uint, nodeID *uint, rx, tx int64, at time.Time) {
	t.Helper()
	if err := db.Create(&storage.TrafficLog{UserID: userID, NodeID: nodeID, RxBytes: rx, TxBytes: tx, RecordedAt: at}).Error; err != nil {
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
	// pricey 节点 2GB（rx+tx）×300 分/GB = 600 分；包月节点不计
	seedTraffic(t, db, u.ID, &pricey.ID, 1<<30, 1<<30, now)
	seedTraffic(t, db, u.ID, &flat.ID, 1<<30, 1<<30, now)
	// 上月同量：month 窗口外不计
	seedTraffic(t, db, u.ID, &pricey.ID, 1<<30, 1<<30, now.AddDate(0, -1, 0))

	cost, err := WindowCostCents(db, u.ID, CycleMonth, now)
	if err != nil {
		t.Fatalf("WindowCostCents: %v", err)
	}
	if cost != 600 {
		t.Fatalf("window cost = %d, want 600", cost)
	}
	// none 全量累计：600+600
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
	seedTraffic(t, db, u.ID, nil, 900, 0, now) // 90% 达阈值

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
	// 留痕带触发口径文案
	if act.Reason == "" {
		t.Fatal("reason should be recorded")
	}
	// 站内信只发一条
	var notes []storage.Notification
	if err := db.Where("user_id = ?", u.ID).Find(&notes).Error; err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Type != storage.NotifSystem {
		t.Fatalf("notifications = %+v", notes)
	}

	// 二轮不重复建不重复发
	if created, err := Sweep(db, now.Add(time.Minute)); err != nil || created != 0 {
		t.Fatalf("second sweep created=%d err=%v, want 0", created, err)
	}
	var n int64
	db.Model(&storage.Notification{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 1 {
		t.Fatalf("notification dup: %d", n)
	}

	// 用量回落 → 释放留痕
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
	// 回落后再触发：新行新通知
	seedTraffic(t, db, u.ID, nil, 950, 0, now.Add(3*time.Minute))
	if created, err := Sweep(db, now.Add(3*time.Minute)); err != nil || created != 1 {
		t.Fatalf("re-trigger created=%d err=%v", created, err)
	}
}

func TestLinkSweepCostTierAndDisable(t *testing.T) {
	db := newLinkDB(t)
	// 不限量用户走费用档
	u := seedLinkUser(t, db, "unlimited", 0)
	setting := LinkSetting{Enabled: true, CostCents: 1000, MaxPriceCents: 0}
	if err := SaveLinkSetting(db, setting); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	seedTraffic(t, db, u.ID, nil, 1<<30, 0, now) // 1GB 无节点归属：单价 0，费用 0 不触发

	if created, err := Sweep(db, now); err != nil || created != 0 {
		t.Fatalf("no-price traffic should not trigger: created=%d err=%v", created, err)
	}

	// 计价流量达阈值 → 费用档触发
	expensive := storage.Node{Name: "exp", Token: "tok-exp", BillingType: "按流量", TrafficPriceCents: 2000}
	if err := db.Create(&expensive).Error; err != nil {
		t.Fatal(err)
	}
	seedTraffic(t, db, u.ID, &expensive.ID, 1<<30, 0, now) // 1GB × 2000 = 2000 分 ≥ 1000
	if created, err := Sweep(db, now); err != nil || created != 1 {
		t.Fatalf("cost tier should trigger: created=%d err=%v", created, err)
	}
	act, err := ActiveAction(db, u.ID)
	if err != nil || act == nil || act.Trigger != LinkTriggerCost {
		t.Fatalf("cost action = %+v err=%v", act, err)
	}
	// 费用档快照
	if act.CostCents != 2000 {
		t.Fatalf("cost snapshot = %d, want 2000", act.CostCents)
	}
	// 关闭联动释放
	if err := SaveLinkSetting(db, LinkSetting{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if created, err := Sweep(db, now.Add(time.Minute)); err != nil || created != 0 {
		t.Fatalf("disable sweep: created=%d err=%v", created, err)
	}
	if act, _ := ActiveAction(db, u.ID); act != nil {
		t.Fatalf("disable should release all, got %+v", act)
	}
	// 禁用用户不参与联动判定（配额超限会触发，但禁用即跳过）
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
