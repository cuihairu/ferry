package billing

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestDetectAnomalies covers the three review classes (F-0588~F-0590): a
// resource without a bill, a bill without a resource, and a month spike.
func TestDetectAnomalies(t *testing.T) {
	db := openBillingDB(t)
	if err := db.Create(&storage.CloudResource{ProviderID: 1, ExternalID: "r-a"}).Error; err != nil {
		t.Fatalf("resource: %v", err)
	}
	if err := db.Create(&storage.CloudResource{ProviderID: 1, ExternalID: "r-b"}).Error; err != nil {
		t.Fatalf("resource: %v", err)
	}
	oct := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	nov := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	recs := []storage.CostRecord{
		{CostProviderID: 1, Source: SourceManual, BillID: "A-1", Currency: "USD", AmountCents: 100,
			IncurredAt: oct, Status: StatusSettled, DedupeKey: RecordKey(SourceManual, 1, "A-1", "")},
		{CostProviderID: 1, Source: SourceManual, BillID: "A-2", Currency: "USD", AmountCents: 300,
			IncurredAt: nov, Status: StatusSettled, DedupeKey: RecordKey(SourceManual, 1, "A-2", "")},
	}
	for i := range recs {
		if err := db.Create(&recs[i]).Error; err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	items := []storage.CostItem{
		// r-a is billed (covered); the ghost id matches no synced resource.
		{CostRecordID: recs[0].ID, ResourceID: "r-a", AmountCents: 100,
			DedupeKey: ItemKey(recs[0].DedupeKey, "l1")},
		{CostRecordID: recs[1].ID, ResourceID: "vm-ghost", AmountCents: 300,
			DedupeKey: ItemKey(recs[1].DedupeKey, "l1")},
	}
	for i := range items {
		if err := db.Create(&items[i]).Error; err != nil {
			t.Fatalf("item %d: %v", i, err)
		}
	}

	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	rows, err := DetectAnomalies(db, from, to, 0)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	kinds := map[string][]string{}
	for _, a := range rows {
		kinds[a.Kind] = append(kinds[a.Kind], a.Target)
	}
	// r-b synced but unbilled (F-0588); r-a covered by its item.
	foundMissing := false
	for _, tgt := range kinds[AnomalyMissingBill] {
		if tgt == "r-b" {
			foundMissing = true
		}
		if tgt == "r-a" {
			t.Fatal("billed resource must not be flagged missing")
		}
	}
	if !foundMissing {
		t.Fatalf("r-b must be flagged missing_bill: %+v", rows)
	}
	// The ghost id is billed but matches no resource (F-0589).
	if len(kinds[AnomalyUnmatchedResource]) != 1 || kinds[AnomalyUnmatchedResource][0] != "vm-ghost" {
		t.Fatalf("vm-ghost must be unmatched: %+v", kinds)
	}
	// 100 → 300 month over month is +200%, over the default 100% (F-0590).
	foundSpike := false
	for _, a := range rows {
		if a.Kind == AnomalySpike {
			foundSpike = true
			if a.ActualCents != 300 || a.ExpectedCents != 100 || a.SpikePct != 200 {
				t.Fatalf("spike frame: %+v", a)
			}
		}
	}
	if !foundSpike {
		t.Fatalf("spike must be detected: %+v", rows)
	}

	// A 300% threshold keeps the same growth unflagged.
	rows, err = DetectAnomalies(db, from, to, 300)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	for _, a := range rows {
		if a.Kind == AnomalySpike {
			t.Fatalf("200%% growth under 300%% threshold must not flag: %+v", a)
		}
	}
}

// TestImportFileFX covers the rate conversion trail (F-0595/F-0596): the
// record keeps the rate, its source and date, and the converted report total.
func TestImportFileFX(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "fx", Source: SourceFile, Currency: "USD"}
	if err := db.Create(&src).Error; err != nil {
		t.Fatalf("source: %v", err)
	}
	raw := []byte(`[
		{"bill_id":"FX-1","item_id":"l1","amount_cents":1000,"currency":"USD",
		 "fx_rate":7.2,"fx_rate_source":"ecb","fx_rate_date":"2026-10-01"}
	]`)
	run, err := New(db, fakeSecrets{}, nil).ImportFile(src.ID, raw, "json", "ops", time.Now())
	if err != nil || run.State != RunSucceeded {
		t.Fatalf("import: run=%+v err=%v", run, err)
	}
	var rec storage.CostRecord
	if err := db.First(&rec).Error; err != nil {
		t.Fatalf("record: %v", err)
	}
	if rec.FXRate != 7.2 || rec.FXRateSource != "ecb" {
		t.Fatalf("fx fields: %+v", rec)
	}
	if rec.FXRateDate == nil || rec.FXRateDate.Format("2006-01-02") != "2026-10-01" {
		t.Fatalf("fx date: %+v", rec.FXRateDate)
	}
	// 1000 minor units × 7.2 = 7200 report units.
	if rec.ReportAmountCents != 7200 {
		t.Fatalf("report cents: %d", rec.ReportAmountCents)
	}
}
