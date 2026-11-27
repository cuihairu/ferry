package billing

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestImportFileLargeBatch drives the full ImportFile path with a 5000-line
// bill (F-2070): the batch must land completely, the run must finish
// succeeded, a re-import of the identical file must dedupe every bill instead
// of double-billing (F-0529), and the whole pass must stay inside a generous
// CI budget. Numbers are archived in docs/testing/scale-report.md.
func TestImportFileLargeBatch(t *testing.T) {
	const lines = 5000
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "aws-scale", Source: SourceManual, Currency: "USD"}
	seedSource(t, db, &src)
	svc := New(db, fakeSecrets{}, nil)
	now := time.Now()

	rows := make([]FileRecord, 0, lines)
	wantCents := int64(0)
	for i := 0; i < lines; i++ {
		cents := int64(100 + i%900)
		wantCents += cents
		rows = append(rows, FileRecord{
			BillID: fmt.Sprintf("INV-%05d", i), ItemID: fmt.Sprintf("l%d", i),
			ResourceID: fmt.Sprintf("vm-%03d", i%200), Category: "instance",
			AmountCents: cents,
			PeriodStart: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
			IncurredAt:  time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
		})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	start := time.Now()
	run, err := svc.ImportFile(src.ID, raw, "json", "scale-tester", now)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if run.State != RunSucceeded || run.Added != lines || run.Skipped != 0 {
		t.Fatalf("run state=%s added=%d skipped=%d (want succeeded/%d/0)", run.State, run.Added, run.Skipped, lines)
	}
	elapsed := time.Since(start)
	if elapsed > 60*time.Second {
		t.Fatalf("5000-line import took %s, outside the CI budget", elapsed)
	}

	var items []storage.CostItem
	if err := db.Find(&items).Error; err != nil {
		t.Fatalf("items: %v", err)
	}
	if len(items) != lines {
		t.Fatalf("items=%d want %d", len(items), lines)
	}
	gotCents := int64(0)
	for _, it := range items {
		gotCents += it.AmountCents
	}
	if gotCents != wantCents {
		t.Fatalf("sum mismatch: got %d want %d", gotCents, wantCents)
	}

	// Same file again: every bill dedupes -- not a cent of double-billing.
	run2, err := svc.ImportFile(src.ID, raw, "json", "scale-tester", now)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if run2.State != RunSucceeded || run2.Added != 0 || run2.Skipped != lines {
		t.Fatalf("re-run state=%s added=%d skipped=%d (want succeeded/0/%d)", run2.State, run2.Added, run2.Skipped, lines)
	}
	var recs []storage.CostRecord
	if err := db.Find(&recs).Error; err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(recs) != lines {
		t.Fatalf("records after re-import=%d want %d (dedupe broken)", len(recs), lines)
	}

	// Cost accounting at volume: the spend aggregation over the imported
	// 5000 records must reproduce the exact cent sum (provider scope).
	spend, err := SpendByScope(db, TargetProvider, nil, nil)
	if err != nil {
		t.Fatalf("spend: %v", err)
	}
	total := int64(0)
	for _, r := range spend {
		total += r.ActualCents
	}
	if total != wantCents {
		t.Fatalf("spend sum mismatch: got %d want %d", total, wantCents)
	}
	t.Logf("5000-line import: first pass %s, re-import all skipped, spend sum %d cents", elapsed, total)
}
