package billing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func openBillingDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, ":memory:")
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

func seedSource(t *testing.T, db *gorm.DB, src *storage.CostProvider) {
	t.Helper()
	if err := db.Create(src).Error; err != nil {
		t.Fatalf("seed source: %v", err)
	}
}

func TestRecordDedupeKey(t *testing.T) {
	if RecordKey("api", 3, "b1", "") != RecordKey("api", 3, "b1", "0") {
		t.Fatal("empty revision must normalize to 0")
	}
	if RecordKey("api", 3, "b1", "") == RecordKey("api", 3, "b1", "1") {
		t.Fatal("revision must change the key")
	}
}

func TestImportFileDedupesAndSnapshots(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "aws", Source: SourceManual, Currency: "USD"}
	seedSource(t, db, &src)
	svc := New(db, fakeSecrets{}, nil)
	now := time.Now()
	row := FileRecord{BillID: "INV-1", ItemID: "l1", ResourceID: "vm-1", Category: "instance",
		AmountCents: 900, PeriodStart: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		IncurredAt: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
	added, updated, skipped, err := svc.storeRows(src, SourceFile, []FileRecord{row}, "tester", now)
	if err != nil || added != 1 || updated != 0 || skipped != 0 {
		t.Fatalf("first import: added=%d updated=%d skipped=%d err=%v", added, updated, skipped, err)
	}
	// Re-import the same bill: deduped, counted as skipped (F-0529).
	added, updated, skipped, err = svc.storeRows(src, SourceFile, []FileRecord{row}, "tester", now)
	if err != nil || added != 0 || skipped != 1 {
		t.Fatalf("reimport: added=%d updated=%d skipped=%d err=%v", added, updated, skipped, err)
	}
	// A revision is a new row (F-0530), not a duplicate.
	rev := row
	rev.Revision = "1"
	added, _, _, err = svc.storeRows(src, SourceFile, []FileRecord{rev}, "tester", now)
	if err != nil || added != 1 {
		t.Fatalf("revision: added=%d err=%v", added, err)
	}
	var recs []storage.CostRecord
	if err := db.Find(&recs).Error; err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(recs) != 2 || recs[0].RawSnapshot == "" {
		t.Fatalf("records=%d raw snapshot missing", len(recs))
	}
	var items []storage.CostItem
	if err := db.Find(&items).Error; err != nil {
		t.Fatalf("items: %v", err)
	}
	if len(items) != 2 || items[0].AmountCents != 900 {
		t.Fatalf("items=%d", len(items))
	}
}

func TestImportFileRejectsBillWithoutID(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "x", Source: SourceManual, Currency: "USD"}
	seedSource(t, db, &src)
	svc := New(db, fakeSecrets{}, nil)
	_, _, skipped, err := svc.storeRows(src, SourceFile, []FileRecord{{AmountCents: 100}}, "t", time.Now())
	if err != nil || skipped != 1 {
		t.Fatalf("blank bill id must be skipped: skipped=%d err=%v", skipped, err)
	}
}

func TestAllocateDirect(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "aws", Source: SourceAPI, Currency: "USD"}
	seedSource(t, db, &src)
	node := storage.Node{Name: "n1", Token: "t", Port: 1, Protocol: "vless"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	key := RecordKey(SourceAPI, src.ID, "INV-2", "0")
	rec := storage.CostRecord{CostProviderID: src.ID, Source: SourceAPI, BillID: "INV-2",
		Currency: "USD", AmountCents: 3000, DedupeKey: key}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("record: %v", err)
	}
	nodeID := node.ID
	item := storage.CostItem{CostRecordID: rec.ID, ResourceID: "vm-9", NodeID: &nodeID,
		Category: CategoryInstance, AmountCents: 3000, DedupeKey: ItemKey(key, "l1")}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("item: %v", err)
	}
	rule := storage.CostAllocationRule{Name: "direct", Kind: BasisDirect, Version: 1, Enabled: true}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("rule: %v", err)
	}
	svc := New(db, fakeSecrets{}, nil)
	rows, err := svc.Allocate(item.ID, rule.ID, 1, time.Now())
	if err != nil || len(rows) != 1 {
		t.Fatalf("allocate: rows=%d err=%v", len(rows), err)
	}
	if rows[0].TargetKind != TargetNode || rows[0].AllocatedCents != 3000 || rows[0].OriginalCents != 3000 {
		t.Fatalf("direct allocation = %+v", rows[0])
	}
	// Same version twice is a no-op (F-0570).
	rows2, err := svc.Allocate(item.ID, rule.ID, 1, time.Now())
	if err != nil || len(rows2) != 0 {
		t.Fatalf("duplicate allocation should be skipped: rows=%d err=%v", len(rows2), err)
	}
}

func TestAllocateRatioSumsToOriginal(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "aws", Source: SourceAPI, Currency: "USD"}
	seedSource(t, db, &src)
	key := RecordKey(SourceAPI, src.ID, "INV-3", "0")
	rec := storage.CostRecord{CostProviderID: src.ID, Source: SourceAPI, BillID: "INV-3",
		Currency: "USD", AmountCents: 1000, DedupeKey: key}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("record: %v", err)
	}
	item := storage.CostItem{CostRecordID: rec.ID, ResourceID: "pool", Category: CategoryBandwidth,
		AmountCents: 1001, DedupeKey: ItemKey(key, "l1")}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("item: %v", err)
	}
	params := RuleParams{Targets: []AllocationTarget{
		{Kind: TargetNode, Key: "1"}, {Kind: TargetNode, Key: "2"},
	}, Ratios: []int64{600_000, 400_000}}
	raw, _ := json.Marshal(params)
	rule := storage.CostAllocationRule{Name: "split", Kind: BasisRatio, Version: 1,
		Enabled: true, Params: string(raw)}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("rule: %v", err)
	}
	svc := New(db, fakeSecrets{}, nil)
	rows, err := svc.Allocate(item.ID, rule.ID, 1, time.Now())
	if err != nil || len(rows) != 2 {
		t.Fatalf("allocate: rows=%d err=%v", len(rows), err)
	}
	var total int64
	for _, r := range rows {
		total += r.AllocatedCents
	}
	if total != item.AmountCents {
		t.Fatalf("ratio split must preserve the original: total=%d want=%d", total, item.AmountCents)
	}
}

func TestAllocateBandwidthWeights(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "aws", Source: SourceAPI, Currency: "USD"}
	seedSource(t, db, &src)
	key := RecordKey(SourceAPI, src.ID, "INV-4", "0")
	rec := storage.CostRecord{CostProviderID: src.ID, Source: SourceAPI, BillID: "INV-4",
		Currency: "USD", AmountCents: 500, DedupeKey: key}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("record: %v", err)
	}
	item := storage.CostItem{CostRecordID: rec.ID, ResourceID: "pool", Category: CategoryBandwidth,
		AmountCents: 500, DedupeKey: ItemKey(key, "l1")}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("item: %v", err)
	}
	params := RuleParams{Targets: []AllocationTarget{
		{Kind: TargetNode, Key: "a"}, {Kind: TargetNode, Key: "b"},
	}, UsageWeight: map[string]int64{"a": 3, "b": 1}}
	raw, _ := json.Marshal(params)
	rule := storage.CostAllocationRule{Name: "bw", Kind: BasisBandwidth, Version: 1,
		Enabled: true, Params: string(raw)}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("rule: %v", err)
	}
	svc := New(db, fakeSecrets{}, nil)
	rows, err := svc.Allocate(item.ID, rule.ID, 1, time.Now())
	if err != nil || len(rows) != 2 {
		t.Fatalf("allocate: rows=%d err=%v", len(rows), err)
	}
	if rows[0].AllocatedCents != 375 || rows[1].AllocatedCents != 125 {
		t.Fatalf("3:1 split of 500 must be 375/125, got %d/%d", rows[0].AllocatedCents, rows[1].AllocatedCents)
	}
}

func TestReconcileDiffAndConfirm(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "aws", Source: SourceAPI, Currency: "USD"}
	seedSource(t, db, &src)
	svc := New(db, fakeSecrets{}, nil)
	now := time.Now()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	in := storage.CostReconciliation{ScopeKind: TargetProvider, TargetID: src.ID,
		PeriodStart: from, PeriodEnd: to, Currency: "USD",
		EstimateCents: 900, ActualCents: 1100}
	row, err := svc.Reconcile(in, now)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if row.DiffCents != 200 || row.DedupeKey == "" {
		t.Fatalf("diff/dedupe: %+v", row)
	}
	// Same scope/period is idempotent: one row, not two (F-0587).
	row2, err := svc.Reconcile(in, now)
	if err != nil || row2.ID != row.ID {
		t.Fatalf("reconcile must be idempotent: %+v err=%v", row2, err)
	}
	confirmed, err := svc.ConfirmReconciliation(row.ID, "confirmed", "vendor added IP fee", "tester")
	if err != nil || confirmed.Status != "confirmed" || confirmed.ApprovedBy != "tester" {
		t.Fatalf("confirm: %+v err=%v", confirmed, err)
	}
}

func TestNewVersionRetiresOldRule(t *testing.T) {
	db := openBillingDB(t)
	rule := storage.CostAllocationRule{Name: "r", Kind: BasisRatio, Version: 1, Enabled: true}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("rule: %v", err)
	}
	svc := New(db, fakeSecrets{}, nil)
	next, err := svc.NewVersion(rule.ID, RuleParams{Category: "traffic"}, "reweight", time.Now())
	if err != nil || next.Version != 2 {
		t.Fatalf("new version: %+v err=%v", next, err)
	}
	var old storage.CostAllocationRule
	if err := db.First(&old, rule.ID).Error; err != nil {
		t.Fatalf("old rule: %v", err)
	}
	if old.Enabled {
		t.Fatal("old rule version must be retired, not deleted")
	}
}

// TestImportFromAPICapabilityFailClosed checks F-0522: a source whose linked
// provider has no billing capability fails the run with the manual hint.
func TestImportFromAPICapabilityFailClosed(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "fake", Source: SourceAPI, Currency: "USD"}
	seedSource(t, db, &src)
	f := cloudprov.NewFake()
	f.SetCaps(cloudprov.Capabilities{APIVersion: "1.0"})
	prov := storage.Provider{Name: "fake", Type: "fake-billing", AccessKey: "sealed", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	db.Model(&storage.CostProvider{}).Where("id = ?", src.ID).Update("provider_id", prov.ID)
	svc := New(db, fakeSecrets{}, func(storage.Provider, string) (cloudprov.Adapter, error) { return f, nil })
	run, err := svc.ImportFromAPI(cancelCtx(), ImportRequest{
		CostProviderID: src.ID, Window: cloudprov.BillingWindow{From: time.Now().Add(-24 * time.Hour)},
	}, "tester", time.Now())
	if err == nil || run == nil || run.State != RunFailed {
		t.Fatalf("capability must fail closed: run=%+v err=%v", run, err)
	}
	if !strings.Contains(run.Error, "manual file import") {
		t.Fatalf("failure hint must mention manual import: %q", run.Error)
	}
}

func TestImportFromAPIPaginates(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "fake", Source: SourceAPI, Currency: "USD"}
	seedSource(t, db, &src)
	f := cloudprov.NewFake()
	now := time.Now()
	var rows []cloudprov.Resource
	for i := 0; i < 5; i++ {
		rows = append(rows, cloudprov.Resource{ResourceID: "vm-" + string(rune('a'+i)), State: "running", BwMbps: 100})
	}
	for _, r := range rows {
		f.Seed(r)
	}
	prov := storage.Provider{Name: "fake", Type: "fake-billing", AccessKey: "sealed", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	db.Model(&storage.CostProvider{}).Where("id = ?", src.ID).Update("provider_id", prov.ID)
	svc := New(db, fakeSecrets{}, func(storage.Provider, string) (cloudprov.Adapter, error) { return f, nil })
	run, err := svc.ImportFromAPI(cancelCtx(), ImportRequest{
		CostProviderID: src.ID, Window: cloudprov.BillingWindow{From: now.Add(-24 * time.Hour)},
		PageSize: 2, Page: 2,
	}, "tester", now)
	if err != nil || run == nil || run.State != RunSucceeded {
		t.Fatalf("paginate: run=%+v err=%v", run, err)
	}
	if run.Fetched != 5 || run.Added != 2 {
		t.Fatalf("page 2 of size 2: fetched=%d added=%d", run.Fetched, run.Added)
	}
	// A page past the end fails the run (F-0527).
	run, err = svc.ImportFromAPI(cancelCtx(), ImportRequest{
		CostProviderID: src.ID, Window: cloudprov.BillingWindow{From: now.Add(-24 * time.Hour)},
		PageSize: 2, Page: 9,
	}, "tester", now)
	if err == nil || run == nil || run.State != RunFailed {
		t.Fatalf("past-end page must fail: run=%+v err=%v", run, err)
	}
}

// fakeSecrets is the in-memory secret seam for tests.
type fakeSecrets struct{}

func (fakeSecrets) Decrypt(string) (string, error) { return "plain", nil }

// cancelCtx keeps the import tests independent of context deadlines.
func cancelCtx() context.Context { return context.Background() }

// TestStorageConstraints covers §4.1 field semantics end to end: the tax
// split (F-0510), timing separation (F-0509), FX fields (F-0508), raw
// snapshot (F-0511) and the unique dedupe constraints (F-0512).
func TestStorageConstraints(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "s", Source: SourceAPI, Currency: "EUR"}
	seedSource(t, db, &src)
	rateDate := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	key := RecordKey(SourceAPI, src.ID, "INV-FX", "0")
	rec := storage.CostRecord{
		CostProviderID: src.ID, Source: SourceAPI, BillID: "INV-FX", Currency: "EUR",
		AmountCents: 1190, PreTaxCents: 1000, TaxCents: 190,
		IncurredAt: rateDate, PeriodStart: rateDate, PeriodEnd: rateDate.AddDate(0, 1, 0),
		ImportedAt: rateDate, Status: StatusSettled, DedupeKey: key,
		RawSnapshot: `{"orig":"payload"}`,
		FXRate:      1.08, FXRateDate: &rateDate, FXRateSource: "ecb", ReportAmountCents: 1285,
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("record: %v", err)
	}
	dup := rec
	dup.ID = 0
	if err := db.Create(&dup).Error; err == nil {
		t.Fatal("duplicate dedupe key must fail (F-0512)")
	}
	var got storage.CostRecord
	if err := db.First(&got, rec.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.TaxCents != 190 || got.PreTaxCents != 1000 || got.FXRateDate == nil || got.RawSnapshot == "" {
		t.Fatalf("tax/FX/snapshot fields lost: %+v", got)
	}
	// Time and resource query indexes exist (F-0513).
	cols := map[string]bool{}
	typ, err := db.DB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	rows, err := typ.Query("SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='cost_records'")
	if err != nil {
		t.Fatalf("indexes: %v", err)
	}
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		cols[name] = true
	}
	rows.Close()
	for _, want := range []string{"idx_cost_records_incurred_at", "idx_cost_records__period_start", "idx_cost_record_dedupe"} {
		found := false
		for name := range cols {
			if strings.Contains(name, strings.TrimPrefix(want, "idx_cost_records__")) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing index matching %q, have %v", want, cols)
		}
	}
}
