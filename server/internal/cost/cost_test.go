package cost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/costref"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// newTestDB 建内存库并迁移。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedNode(t *testing.T, db *gorm.DB, name, region, billing string, priceCents, monthlyCents int64) storage.Node {
	t.Helper()
	n := storage.Node{Name: name, Token: "tok-" + name, Region: region, ISP: "测试ISP",
		BillingType: billing, TrafficPriceCents: priceCents, MonthlyCostCents: monthlyCents, Enabled: true}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func seedTraffic(t *testing.T, db *gorm.DB, node uint, rx, tx int64, at time.Time) {
	t.Helper()
	if err := db.Create(&storage.NodeTrafficLog{NodeID: node, Proc: "xray", RxBytes: rx, TxBytes: tx, RecordedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
}

// TestBuild 覆盖成本核算：流量花费只算按流量节点、月度预估折算、维度汇总。
func TestBuild(t *testing.T) {
	db := newTestDB(t)
	// 10GB × 100分/GB = 1000 分流量费；包月节点不计流量费。
	metered := seedNode(t, db, "metered", "hk", "按流量", 100, 0)
	seedNode(t, db, "fixed", "hk", "包月", 0, 3000)
	oversea := seedNode(t, db, "oversea", "us", "按流量", 50, 2000)

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) // 月过一半
	seedTraffic(t, db, metered.ID, 5e9, 5e9, now.Add(-time.Hour))
	seedTraffic(t, db, oversea.ID, 1e9, 0, now.Add(-time.Hour))
	// 上月流量不计入
	lastMonth := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	seedTraffic(t, db, metered.ID, 100e9, 0, lastMonth)

	rep, err := Build(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Nodes) != 3 {
		t.Fatalf("nodes = %d", len(rep.Nodes))
	}
	byName := map[string]NodeCost{}
	for _, nc := range rep.Nodes {
		byName[nc.Name] = nc
	}
	if got := byName["metered"].TrafficCostCents; got != 1000 {
		t.Fatalf("metered traffic cost = %d, want 1000", got)
	}
	if got := byName["fixed"].TrafficCostCents; got != 0 {
		t.Fatalf("fixed node must have no traffic cost, got %d", got)
	}
	if got := byName["fixed"].FixedCostCents; got != 3000 {
		t.Fatalf("fixed cost = %d", got)
	}
	// 月过一半（约 15.5/31），流量费 50 分 → 预估 ≈ 2000+100=2100（容差）
	if got := byName["oversea"].ProjectedCents; got < 2050 || got > 2150 {
		t.Fatalf("oversea projected = %d, want ~2100", got)
	}
	// 区域汇总：hk 两节点，us 一节点
	if len(rep.Regions) != 2 {
		t.Fatalf("regions = %d", len(rep.Regions))
	}
	// 排序按预估降序：us（oversea 2100）在 hk（3000+1000/0.5=5000?）——算一遍：
	// hk = fixed 3000 + metered(1000/0.5=2000) = 5000，hk 应在先。
	if rep.Regions[0].Key != "hk" || rep.Regions[0].Nodes != 2 {
		t.Fatalf("regions[0] = %+v", rep.Regions[0])
	}
	if rep.Summary.TrafficCents != 1050 {
		t.Fatalf("summary traffic = %d", rep.Summary.TrafficCents)
	}
}

func TestMonthElapsedFraction(t *testing.T) {
	// 首小时不外推；月中按比例。
	if f := monthElapsedFraction(time.Date(2026, 10, 1, 0, 30, 0, 0, time.UTC)); f != 1 {
		t.Fatalf("first-hour fraction = %f, want 1", f)
	}
	f := monthElapsedFraction(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	if f < 0.48 || f > 0.53 {
		t.Fatalf("mid-month fraction = %f", f)
	}
	if f := monthElapsedFraction(time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC)); f < 0.99 {
		t.Fatalf("month-end fraction = %f", f)
	}
}

func TestThresholdPersistence(t *testing.T) {
	db := newTestDB(t)
	if v, err := GetThreshold(db); err != nil || v != DefaultThresholdCents {
		t.Fatalf("default threshold = %d err=%v", v, err)
	}
	if err := SaveThreshold(db, 8000); err != nil {
		t.Fatal(err)
	}
	if v, _ := GetThreshold(db); v != 8000 {
		t.Fatalf("threshold = %d", v)
	}
	if err := SaveThreshold(db, -1); err == nil {
		t.Fatal("negative threshold must be rejected")
	}
}

// TestCheckAlerts 覆盖高成本告警：超阈值单发去重、回落自动消解。
func TestCheckAlerts(t *testing.T) {
	db := newTestDB(t)
	metered := seedNode(t, db, "metered", "hk", "按流量", 100, 0)
	seedNode(t, db, "fixed", "hk", "包月", 0, 99900) // 包月再贵也不告警
	if err := SaveThreshold(db, 5000); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	// 100GB × 100分 = 10000 分 > 5000 阈值
	seedTraffic(t, db, metered.ID, 60e9, 40e9, now.Add(-time.Hour))

	logger := log.New(&bytes.Buffer{}, "", 0)
	if err := CheckAlerts(db, now, logger); err != nil {
		t.Fatal(err)
	}
	var alerts []storage.Alert
	if err := db.Where("kind = ?", AlertKindHighCost).Find(&alerts).Error; err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].NodeID != metered.ID || alerts[0].State != "active" {
		t.Fatalf("alerts = %+v", alerts)
	}
	// 告警激活同落 cost_exceeded 事件（HERALD-3 余量），包月不落
	var evs []storage.Event
	if err := db.Where("kind = ?", "cost_exceeded").Find(&evs).Error; err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Severity != "warning" ||
		evs[0].DedupKey != fmt.Sprintf("node:%d:cost_exceeded", metered.ID) {
		t.Fatalf("cost_exceeded events = %+v", evs)
	}

	// 复跑去重：仍只有一条。
	if err := CheckAlerts(db, now, logger); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := db.Model(&storage.Alert{}).Where("kind = ?", AlertKindHighCost).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("duplicate alert, n = %d", n)
	}
	var evN int64
	db.Model(&storage.Event{}).Where("kind = ?", "cost_exceeded").Count(&evN)
	if evN != 1 {
		t.Fatalf("duplicate cost_exceeded event, n = %d", evN)
	}

	// 阈值调高到花费之上 → 回落自动消解。
	if err := SaveThreshold(db, 20000); err != nil {
		t.Fatal(err)
	}
	if err := CheckAlerts(db, now, logger); err != nil {
		t.Fatal(err)
	}
	var resolved storage.Alert
	if err := db.Where("kind = ?", AlertKindHighCost).First(&resolved).Error; err != nil {
		t.Fatal(err)
	}
	if resolved.State != "resolved" || resolved.ResolvedAt == nil {
		t.Fatalf("alert should auto-resolve: %+v", resolved)
	}
}

// TestNodeQueryAndSpecOf 覆盖牌价键映射：缺机房拒绝、区域与档位透传；
// SpecOf 拼「带宽-流量」档、零段省略。
func TestNodeQueryAndSpecOf(t *testing.T) {
	if _, err := NodeQuery(storage.Node{}); err == nil {
		t.Fatal("node without datacenter should error")
	}
	q, err := NodeQuery(storage.Node{Datacenter: "vultr", Region: "tokyo",
		BwDownMbps: 500, MonthlyTrafficQuotaBytes: 2 * BytesPerGB})
	if err != nil {
		t.Fatal(err)
	}
	if q.Provider != "vultr" || q.Region != "tokyo" || q.Spec != "500M-2G" {
		t.Fatalf("query = %+v; want vultr/tokyo/500M-2G", q)
	}
	if got := SpecOf(0, 0); got != "" {
		t.Fatalf("SpecOf zero = %q; want empty", got)
	}
	if got := SpecOf(100, 0); got != "100M" {
		t.Fatalf("SpecOf bw only = %q; want 100M", got)
	}
	if got := SpecOf(0, 3*BytesPerGB); got != "3G" {
		t.Fatalf("SpecOf quota only = %q; want 3G", got)
	}
}

// TestCheckNode 单节点对账：命中牌价算偏差、超容忍度提示人工复核、
// 查不到/手录未填/币种不符各回错误。
func TestCheckNode(t *testing.T) {
	src := costref.NewStatic("pricelist", []costref.Entry{
		{Provider: "vultr", Region: "tokyo", Spec: "500M-2G", MonthlyCents: 10000, Currency: "CNY"},
	})
	base := storage.Node{Datacenter: "vultr", Region: "tokyo",
		BwDownMbps: 500, MonthlyTrafficQuotaBytes: 2 * BytesPerGB}

	n := base
	n.MonthlyCostCents, n.Currency = 9000, "CNY"
	quote, dev, err := CheckNode(src, n, 30)
	if err != nil {
		t.Fatal(err)
	}
	if quote.MonthlyCents != 10000 || dev.Pct != -10 || dev.Off {
		t.Fatalf("quote=%+v dev=%+v; want 10000 / -10%% / off=false", quote, dev)
	}

	n.MonthlyCostCents = 15000
	_, dev, err = CheckNode(src, n, 30)
	if err != nil || !dev.Off {
		t.Fatalf("over tolerance = %+v, %v; want off", dev, err)
	}

	n.MonthlyCostCents = 0
	if _, _, err := CheckNode(src, n, 30); !errors.Is(err, costref.ErrNoManual) {
		t.Fatalf("no manual = %v; want ErrNoManual", err)
	}
	n.MonthlyCostCents, n.Currency = 9000, "USD"
	if _, _, err := CheckNode(src, n, 30); !errors.Is(err, costref.ErrCurrencyMismatch) {
		t.Fatalf("currency mismatch = %v; want ErrCurrencyMismatch", err)
	}
	if _, _, err := CheckNode(src, storage.Node{Datacenter: "ghost"}, 30); !errors.Is(err, costref.ErrNotFound) {
		t.Fatalf("no quote = %v; want ErrNotFound", err)
	}
}

// TestLoopChecksAlertsUntilCancel 覆盖 Loop 启停：启动即跑一轮告警检查
// （超阈值节点落 active 告警），ctx 取消后退出；nil logger 兜底。
func TestLoopChecksAlertsUntilCancel(t *testing.T) {
	db := newTestDB(t)
	metered := seedNode(t, db, "metered", "hk", "按流量", 100, 0)
	if err := SaveThreshold(db, 5000); err != nil {
		t.Fatal(err)
	}
	seedTraffic(t, db, metered.ID, 60e9, 40e9, time.Now().Add(-time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 20*time.Millisecond, nil)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var alerts []storage.Alert
		if err := db.Where("kind = ?", AlertKindHighCost).Find(&alerts).Error; err != nil {
			t.Fatalf("query alerts: %v", err)
		}
		if len(alerts) == 1 && alerts[0].State == "active" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("loop first check never alerted: %+v", alerts)
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

// TestWatchLoopScansUntilCancel 覆盖 WatchLoop 启停：导入价格表+关注行，
// 启动即扫出快照，ctx 取消后退出。
func TestWatchLoopScansUntilCancel(t *testing.T) {
	db := newTestDB(t)
	if err := SaveRefTable(db, []byte(`[{"provider":"vultr","region":"tokyo","spec":"1c","monthly_cents":1000}]`)); err != nil {
		t.Fatal(err)
	}
	seedWatch(t, db, "vultr", "tokyo", "1c", 900)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		WatchLoop(ctx, db, 20*time.Millisecond, nil)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var snaps []storage.PriceSnapshot
		if err := db.Find(&snaps).Error; err != nil {
			t.Fatalf("query snapshots: %v", err)
		}
		if len(snaps) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watch loop first scan never landed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watch loop did not return after cancel")
	}
}
