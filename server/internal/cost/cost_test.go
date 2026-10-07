package cost

import (
	"bytes"
	"log"
	"testing"
	"time"

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
