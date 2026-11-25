package billing

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// seedSpendData lays out three records across two accounts, two regions, two
// currencies and two months, plus an item pair, one node allocation and a
// path estimate pair (one known, one unknown).
func seedSpendData(t *testing.T, db *gorm.DB) (srcID uint) {
	t.Helper()
	src := storage.CostProvider{Name: "vendor-a", Source: SourceManual, Currency: "USD"}
	if err := db.Create(&src).Error; err != nil {
		t.Fatalf("source: %v", err)
	}
	oct := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	nov := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	recs := []storage.CostRecord{
		{CostProviderID: src.ID, Source: SourceManual, BillID: "B-1", Account: "acc-1", Region: "us-1",
			Currency: "USD", AmountCents: 1000, IncurredAt: oct, PeriodStart: oct, PeriodEnd: nov.AddDate(0, 0, -4),
			Status: StatusSettled, DedupeKey: RecordKey(SourceManual, src.ID, "B-1", "")},
		{CostProviderID: src.ID, Source: SourceManual, BillID: "B-2", Account: "acc-1", Region: "us-2",
			Currency: "USD", AmountCents: 500, IncurredAt: nov, PeriodStart: nov, PeriodEnd: nov.AddDate(0, 1, 0),
			Status: StatusSettled, DedupeKey: RecordKey(SourceManual, src.ID, "B-2", "")},
		{CostProviderID: src.ID, Source: SourceManual, BillID: "B-3", Account: "acc-2", Region: "us-1",
			Currency: "CNY", AmountCents: 200, IncurredAt: oct.Add(5 * 24 * time.Hour), PeriodStart: oct, PeriodEnd: nov.AddDate(0, 0, -4),
			Status: StatusSettled, DedupeKey: RecordKey(SourceManual, src.ID, "B-3", "")},
	}
	for i := range recs {
		if err := db.Create(&recs[i]).Error; err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	items := []storage.CostItem{
		{CostRecordID: recs[0].ID, ResourceID: "vm-x", Region: "us-1", Category: CategoryInstance,
			AmountCents: 600, DedupeKey: ItemKey(recs[0].DedupeKey, "l1")},
		{CostRecordID: recs[0].ID, ResourceID: "vm-y", Region: "us-1", Category: CategoryTraffic,
			AmountCents: 400, DedupeKey: ItemKey(recs[0].DedupeKey, "l2")},
	}
	for i := range items {
		if err := db.Create(&items[i]).Error; err != nil {
			t.Fatalf("item %d: %v", i, err)
		}
	}
	alloc := storage.CostAllocation{CostItemID: items[0].ID, RuleID: 1, RuleVersion: 1,
		TargetKind: TargetNode, TargetID: 7, Basis: BasisDirect,
		OriginalCents: 600, AllocatedCents: 600,
		DedupeKey: AllocationKey(items[0].DedupeKey, 1, 1, TargetNode, "7")}
	if err := db.Create(&alloc).Error; err != nil {
		t.Fatalf("allocation: %v", err)
	}
	path9 := uint(9)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	est := storage.CostEstimate{PathID: &path9, Kind: KindBandwidth, AmountCents: 300, Currency: "USD",
		PriceSource: "hand", PeriodStart: from, PeriodEnd: to,
		DedupeKey: EstimateKey(KindBandwidth, nil, &path9, "", from, to)}
	if err := db.Create(&est).Error; err != nil {
		t.Fatalf("estimate: %v", err)
	}
	unknown := storage.CostEstimate{PathID: &path9, Kind: KindDisk, AmountCents: 0, Currency: "USD",
		UnknownPrice: true, PeriodStart: from, PeriodEnd: to,
		DedupeKey: EstimateKey(KindDisk, nil, &path9, "", from, to)}
	if err := db.Create(&unknown).Error; err != nil {
		t.Fatalf("unknown estimate: %v", err)
	}
	return src.ID
}

func spendSums(rows []SpendRow, currency string) (actual, allocated, estimate int64, count int64) {
	for _, r := range rows {
		if r.Currency != currency {
			continue
		}
		actual += r.ActualCents
		allocated += r.AllocatedCents
		estimate += r.EstimateCents
		count += r.Count
	}
	return
}

// TestSpendByScope covers the six aggregation dimensions (F-0581~F-0586) with
// per-currency sums (F-0594).
func TestSpendByScope(t *testing.T) {
	db := openBillingDB(t)
	seedSpendData(t, db)

	// Provider (F-0581): USD 1500 across two bills, CNY 200 separately.
	rows, err := SpendByScope(db, TargetProvider, nil, nil)
	if err != nil {
		t.Fatalf("provider scope: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 currency rows, got %d: %+v", len(rows), rows)
	}
	if actual, _, _, count := spendSums(rows, "USD"); actual != 1500 || count != 2 {
		t.Fatalf("provider USD: actual=%d count=%d", actual, count)
	}
	for _, r := range rows {
		if r.Currency == "USD" && r.Name != "vendor-a" {
			t.Fatalf("provider name: %+v", r)
		}
	}

	// Account (F-0582).
	rows, err = SpendByScope(db, TargetAccount, nil, nil)
	if err != nil {
		t.Fatalf("account scope: %v", err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		if r.Currency == "USD" {
			got[r.Key] = r.ActualCents
		}
	}
	if got["acc-1"] != 1500 || got["acc-2"] != 0 {
		t.Fatalf("account sums: %+v", got)
	}

	// Region (F-0583), bounded to October: us-2 (November) drops out.
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC)
	rows, err = SpendByScope(db, TargetRegion, &from, &to)
	if err != nil {
		t.Fatalf("region scope: %v", err)
	}
	regions := map[string]int64{}
	for _, r := range rows {
		if r.Currency == "USD" {
			regions[r.Key] = r.ActualCents
		}
	}
	if regions["us-1"] != 1000 {
		t.Fatalf("region us-1 must be 1000: %+v", regions)
	}
	if _, ok := regions["us-2"]; ok {
		t.Fatalf("November region must be excluded: %+v", regions)
	}

	// Resource (F-0584).
	rows, err = SpendByScope(db, TargetResource, nil, nil)
	if err != nil {
		t.Fatalf("resource scope: %v", err)
	}
	res := map[string]int64{}
	for _, r := range rows {
		res[r.Key] = r.ActualCents
	}
	if res["vm-x"] != 600 || res["vm-y"] != 400 {
		t.Fatalf("resource sums: %+v", res)
	}

	// Node allocations (F-0585).
	rows, err = SpendByScope(db, TargetNode, nil, nil)
	if err != nil {
		t.Fatalf("node scope: %v", err)
	}
	if len(rows) != 1 || rows[0].TargetID != 7 || rows[0].AllocatedCents != 600 || rows[0].Currency != "USD" {
		t.Fatalf("node allocation row: %+v", rows)
	}

	// Path estimates (F-0586): the unknown-price line must not sum in.
	rows, err = SpendByScope(db, TargetPath, nil, nil)
	if err != nil {
		t.Fatalf("path scope: %v", err)
	}
	if len(rows) != 1 || rows[0].EstimateCents != 300 {
		t.Fatalf("path estimate row: %+v", rows)
	}

	// Invalid scope is rejected, never silently summed.
	if _, err := SpendByScope(db, "galaxy", nil, nil); err == nil {
		t.Fatal("invalid scope must error")
	}
}

// TestSpendTrend covers the day/month/cycle bucketing (F-0593) with
// per-currency rows (F-0594).
func TestSpendTrend(t *testing.T) {
	db := openBillingDB(t)
	seedSpendData(t, db)

	rows, err := SpendTrend(db, "month", nil, nil)
	if err != nil {
		t.Fatalf("month trend: %v", err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Bucket+"|"+r.Currency] += r.ActualCents
	}
	if got["2026-10|USD"] != 1000 || got["2026-11|USD"] != 500 || got["2026-10|CNY"] != 200 {
		t.Fatalf("month buckets: %+v", got)
	}

	rows, err = SpendTrend(db, "day", nil, nil)
	if err != nil {
		t.Fatalf("day trend: %v", err)
	}
	got = map[string]int64{}
	for _, r := range rows {
		got[r.Bucket+"|"+r.Currency] += r.ActualCents
	}
	if got["2026-10-15|USD"] != 1000 {
		t.Fatalf("day buckets: %+v", got)
	}

	rows, err = SpendTrend(db, "cycle", nil, nil)
	if err != nil {
		t.Fatalf("cycle trend: %v", err)
	}
	if len(rows) < 3 {
		t.Fatalf("cycle buckets must keep the vendor periods: %+v", rows)
	}

	if _, err := SpendTrend(db, "week", nil, nil); err == nil {
		t.Fatal("invalid bucket must error")
	}
}
