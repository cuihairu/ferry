package billing

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func openEstimateDB(t *testing.T) *gorm.DB {
	t.Helper()
	return openBillingDB(t)
}

// TestEstimateBaseKinds covers the base billing estimate (F-0541~F-0544): a
// monthly node and a flat-bandwidth node both estimate from the hand-entered
// monthly cost; a node with no price filled in is marked unknown (F-0551).
func TestEstimateBaseKinds(t *testing.T) {
	db := openEstimateDB(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	monthly := storage.Node{Name: "m", Token: "tk-m", BillingType: "包月", MonthlyCostCents: 3000, Currency: "CNY", Region: "cn-1"}
	if err := db.Create(&monthly).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	rows, err := EstimateNode(db, &monthly, from, to)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != KindInstance || rows[0].AmountCents != 3000 || rows[0].UnknownPrice {
		t.Fatalf("monthly estimate: %+v", rows)
	}
	if rows[0].PriceSource != priceNodeMonthly {
		t.Fatalf("price source: %q", rows[0].PriceSource)
	}

	// Flat bandwidth bills like monthly (design 套餐与成本 §2, F-0544).
	flat := storage.Node{Name: "f", Token: "tk-f", BillingType: "固定带宽", MonthlyCostCents: 2000, Currency: "CNY"}
	if err := db.Create(&flat).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	rows, err = EstimateNode(db, &flat, from, to)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != KindInstance || rows[0].AmountCents != 2000 {
		t.Fatalf("flat-bandwidth estimate: %+v", rows)
	}

	// No price filled in: unknown sentinel, never an invented number (F-0551).
	free := storage.Node{Name: "u", Token: "tk-u", BillingType: "包月", Currency: "CNY"}
	if err := db.Create(&free).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	rows, err = EstimateNode(db, &free, from, to)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if len(rows) != 1 || !rows[0].UnknownPrice || rows[0].AmountCents != 0 {
		t.Fatalf("unknown estimate must be flagged: %+v", rows)
	}
}

// TestEstimateHourly covers hourly-billed resources (F-0542): the line's
// hourly price times the period hours.
func TestEstimateHourly(t *testing.T) {
	db := openEstimateDB(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(12 * time.Hour)

	node := storage.Node{Name: "h", Token: "tk-h", BillingType: "包月", Currency: "CNY"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	line := storage.NodeCostLine{NodeID: node.ID, Kind: KindHourly, HourlyPriceCents: 10, Currency: "CNY", Enabled: true}
	if err := db.Create(&line).Error; err != nil {
		t.Fatalf("line: %v", err)
	}
	rows, err := EstimateNode(db, &node, from, to)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	var hourly *storage.CostEstimate
	for i := range rows {
		if rows[i].Kind == KindHourly {
			hourly = &rows[i]
		}
	}
	if hourly == nil || hourly.AmountCents != 120 {
		t.Fatalf("hourly estimate must be 10x12h=120: %+v", rows)
	}

	// No hourly price: unknown sentinel (F-0551).
	line.HourlyPriceCents = 0
	if err := db.Save(&line).Error; err != nil {
		t.Fatalf("save: %v", err)
	}
	rows, err = EstimateNode(db, &node, from, to)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	for _, r := range rows {
		if r.Kind == KindHourly && (!r.UnknownPrice || r.AmountCents != 0) {
			t.Fatalf("unknown hourly must be flagged zero: %+v", r)
		}
	}
}

// TestEstimateTraffic covers traffic-billed nodes (F-0543): booked period
// traffic times the hand-entered unit price.
func TestEstimateTraffic(t *testing.T) {
	db := openEstimateDB(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	node := storage.Node{Name: "t", Token: "tk-t", BillingType: "按流量", TrafficPriceCents: 100, Currency: "CNY"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	logs := []storage.NodeTrafficLog{
		{NodeID: node.ID, Proc: "x", RxBytes: BytesPerGB, TxBytes: 0, RecordedAt: from.Add(time.Hour)},
		{NodeID: node.ID, Proc: "x", RxBytes: 0, TxBytes: BytesPerGB, RecordedAt: from.Add(2 * time.Hour)},
	}
	for i := range logs {
		if err := db.Create(&logs[i]).Error; err != nil {
			t.Fatalf("log: %v", err)
		}
	}
	rows, err := EstimateNode(db, &node, from, to)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != KindTraffic || rows[0].AmountCents != 200 || rows[0].UnknownPrice {
		t.Fatalf("traffic estimate must be 2GB x 100 = 200: %+v", rows)
	}
}

// TestEstimateLinesProrate covers the cost-line classes (F-0545~F-0549): extra
// IP, disk, snapshot, backup and custom prorate their monthly amount into the
// period; discount lines keep the negative sign (F-0548).
func TestEstimateLinesProrate(t *testing.T) {
	db := openEstimateDB(t)
	// Half of October: 15/31 of each monthly amount.
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)

	node := storage.Node{Name: "p", Token: "tk-p", BillingType: "包月", MonthlyCostCents: 3100, Currency: "CNY"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	lines := []storage.NodeCostLine{
		{NodeID: node.ID, Kind: KindIP, AmountCents: 310, Currency: "CNY", Enabled: true, Note: "extra ip x1"},
		{NodeID: node.ID, Kind: KindDisk, AmountCents: 620, Currency: "CNY", Enabled: true},
		{NodeID: node.ID, Kind: KindSnapshot, AmountCents: 124, Currency: "CNY", Enabled: true},
		{NodeID: node.ID, Kind: KindBackup, AmountCents: 248, Currency: "CNY", Enabled: true},
		{NodeID: node.ID, Kind: KindCustom, AmountCents: 62, Currency: "CNY", Enabled: true, Note: "license"},
		{NodeID: node.ID, Kind: KindDiscount, AmountCents: -3100, Currency: "CNY", Enabled: true, Note: "reserved 1y"},
	}
	for i := range lines {
		if err := db.Create(&lines[i]).Error; err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
	}
	rows, err := EstimateNode(db, &node, from, to)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if len(rows) != len(lines)+1 {
		t.Fatalf("want %d rows, got %d: %+v", len(lines)+1, len(rows), rows)
	}
	byKind := map[string]int64{}
	for _, r := range rows {
		byKind[r.Kind] = r.AmountCents
	}
	// 15/31 of each monthly amount, integer-truncated.
	want := map[string]int64{
		KindInstance: 1500, KindIP: 150, KindDisk: 300, KindSnapshot: 60,
		KindBackup: 120, KindCustom: 30, KindDiscount: -1500,
	}
	for kind, amount := range want {
		if byKind[kind] != amount {
			t.Fatalf("kind %s: want %d, got %d", kind, amount, byKind[kind])
		}
	}
}

// TestEstimateUnknownLine covers the unknown-price sentinel on lines (F-0551).
func TestEstimateUnknownLine(t *testing.T) {
	db := openEstimateDB(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	node := storage.Node{Name: "u", Token: "tk-u", BillingType: "包月", MonthlyCostCents: 1000, Currency: "CNY"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	line := storage.NodeCostLine{NodeID: node.ID, Kind: KindBackup, UnknownPrice: true, AmountCents: 500, Currency: "CNY", Enabled: true}
	if err := db.Create(&line).Error; err != nil {
		t.Fatalf("line: %v", err)
	}
	rows, err := EstimateNode(db, &node, from, to)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	for _, r := range rows {
		if r.Kind == KindBackup && (!r.UnknownPrice || r.AmountCents != 0) {
			t.Fatalf("unknown-price line must store zero flagged: %+v", r)
		}
	}
}

// TestEstimateRecomputeUpdates covers recomputation: same scope/kind/period
// refreshes the row in place instead of duplicating it.
func TestEstimateRecomputeUpdates(t *testing.T) {
	db := openEstimateDB(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	node := storage.Node{Name: "r", Token: "tk-r", BillingType: "包月", MonthlyCostCents: 1000, Currency: "CNY"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	if _, err := EstimateNode(db, &node, from, to); err != nil {
		t.Fatalf("estimate: %v", err)
	}
	node.MonthlyCostCents = 2000
	if err := db.Model(&storage.Node{}).Where("id = ?", node.ID).Update("monthly_cost_cents", 2000).Error; err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := EstimateNode(db, &node, from, to); err != nil {
		t.Fatalf("re-estimate: %v", err)
	}
	var rows []storage.CostEstimate
	if err := db.Where("node_id = ?", node.ID).Find(&rows).Error; err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].AmountCents != 2000 || rows[0].UnknownPrice {
		t.Fatalf("recompute must refresh in place: %+v", rows)
	}
}

// TestEstimateAll covers the bulk path over every node.
func TestEstimateAll(t *testing.T) {
	db := openEstimateDB(t)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	for _, n := range []storage.Node{
		{Name: "a", Token: "tk-a", BillingType: "包月", MonthlyCostCents: 1000, Currency: "CNY"},
		{Name: "b", Token: "tk-b", BillingType: "按流量", TrafficPriceCents: 50, Currency: "CNY"},
	} {
		if err := db.Create(&n).Error; err != nil {
			t.Fatalf("node: %v", err)
		}
	}
	count, err := EstimateAll(db, from, to)
	if err != nil {
		t.Fatalf("estimate all: %v", err)
	}
	if count != 2 {
		t.Fatalf("want 2 rows, got %d", count)
	}
}
