package billing

import (
	"context"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
)

// histSrcCount reads a labelled histogram child's _count from its exposition.
func histSrcCount(t *testing.T, source string) float64 {
	t.Helper()
	out, err := testutil.CollectAndFormat(syncSeconds, expfmt.TypeTextPlain,
		"ferry_billing_sync_seconds")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^` +
		regexp.QuoteMeta(`ferry_billing_sync_seconds_count{source="`+source+`"}`) + ` ([0-9.eE+-]+)$`)
	m := re.FindStringSubmatch(string(out))
	if m == nil {
		return 0 // series created on first observation
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("source %s count %q: %v", source, m[1], err)
	}
	return v
}

// TestBillingSyncMetrics covers F-1813: every import run outcome is counted
// per source (api/file) with its wall time; success rate derives from the
// counter at query time.
func TestBillingSyncMetrics(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "fake", Source: SourceAPI, Currency: "USD"}
	seedSource(t, db, &src)

	okChild := syncTotal.WithLabelValues(SourceAPI, RunSucceeded)
	failChild := syncTotal.WithLabelValues(SourceAPI, RunFailed)
	fileFailChild := syncTotal.WithLabelValues(SourceFile, RunFailed)
	okBefore, failBefore, fileFailBefore :=
		testutil.ToFloat64(okChild), testutil.ToFloat64(failChild), testutil.ToFloat64(fileFailChild)
	countBefore := histSrcCount(t, SourceAPI)

	f := cloudprov.NewFake()
	f.SetCaps(cloudprov.Capabilities{APIVersion: "1.0", Billing: true})
	f.Seed(cloudprov.Resource{ResourceID: "vm-b1", State: "running", BwMbps: 100})
	prov := storage.Provider{Name: "fake", Type: "fake-billing", AccessKey: "sealed", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	db.Model(&storage.CostProvider{}).Where("id = ?", src.ID).Update("provider_id", prov.ID)
	svc := New(db, fakeSecrets{}, func(storage.Provider, string) (cloudprov.Adapter, error) { return f, nil })
	ctx := context.Background()

	// A successful API pull: counted succeeded with one latency observation.
	run, err := svc.ImportFromAPI(ctx, ImportRequest{
		CostProviderID: src.ID, Window: cloudprov.BillingWindow{From: time.Now().Add(-24 * time.Hour)},
	}, "tester", time.Now())
	if err != nil || run.State != RunSucceeded {
		t.Fatalf("api import: run=%+v err=%v", run, err)
	}
	if got := testutil.ToFloat64(okChild) - okBefore; got != 1 {
		t.Fatalf("succeeded delta = %v, want 1", got)
	}
	if got := histSrcCount(t, SourceAPI) - countBefore; got != 1 {
		t.Fatalf("histogram count delta = %v, want 1", got)
	}

	// A capability-less vendor fails the run closed: counted failed, latency
	// still observed (the run happened, it just failed early).
	f2 := cloudprov.NewFake()
	f2.SetCaps(cloudprov.Capabilities{APIVersion: "1.0"})
	svc2 := New(db, fakeSecrets{}, func(storage.Provider, string) (cloudprov.Adapter, error) { return f2, nil })
	run, err = svc2.ImportFromAPI(ctx, ImportRequest{
		CostProviderID: src.ID, Window: cloudprov.BillingWindow{From: time.Now().Add(-24 * time.Hour)},
	}, "tester", time.Now())
	if err == nil || run.State != RunFailed {
		t.Fatalf("capability must fail closed: run=%+v err=%v", run, err)
	}
	if got := testutil.ToFloat64(failChild) - failBefore; got != 1 {
		t.Fatalf("failed delta = %v, want 1", got)
	}

	// A malformed file import counts under the file source.
	if _, err := svc.ImportFile(src.ID, []byte(`not json`), "json", "tester", time.Now()); err == nil {
		t.Fatal("bad file must fail")
	}
	if got := testutil.ToFloat64(fileFailChild) - fileFailBefore; got != 1 {
		t.Fatalf("file failed delta = %v, want 1", got)
	}
}
