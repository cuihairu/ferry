package billing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestBillingAuthFailureReports covers F-1828's billing leg: an ImportFromAPI
// whose vendor rejects the credentials reports the auth failure with the
// source's provider identity (the billing service calls adapters directly,
// so it reports on its own two call sites); a capability-less failure and a
// clean run stay silent.
func TestBillingAuthFailureReports(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "fake", Source: SourceAPI, Currency: "USD"}
	seedSource(t, db, &src)
	prov := storage.Provider{Name: "prov9", Type: "fake", AccessKey: "sealed", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	db.Model(&storage.CostProvider{}).Where("id = ?", src.ID).Update("provider_id", prov.ID)

	var got []cloudprov.AuthFailure
	cloudprov.SetAuthFailureHook(func(f cloudprov.AuthFailure) { got = append(got, f) })
	defer cloudprov.SetAuthFailureHook(nil)

	f := cloudprov.NewFake()
	f.SetCaps(cloudprov.Capabilities{APIVersion: "1.0", Billing: true})
	f.SetFailHook(cloudprov.OpImportBilling, func() *cloudprov.Error {
		return cloudprov.NewError(cloudprov.ErrAuth, cloudprov.OpImportBilling, "", "401", "signature mismatch")
	})
	svc := New(db, fakeSecrets{}, func(storage.Provider, string) (cloudprov.Adapter, error) { return f, nil })

	run, err := svc.ImportFromAPI(context.Background(), ImportRequest{
		CostProviderID: src.ID, Window: cloudprov.BillingWindow{From: time.Now().Add(-24 * time.Hour)},
	}, "tester", time.Now())
	if err == nil || run.State != RunFailed {
		t.Fatalf("auth failure must fail the run: run=%+v err=%v", run, err)
	}
	if len(got) != 1 {
		t.Fatalf("auth reports = %d, want 1", len(got))
	}
	if got[0].ProviderID != prov.ID || got[0].Op != cloudprov.OpImportBilling ||
		got[0].ProviderCode != "401" {
		t.Fatalf("report payload = %+v", got[0])
	}

	// A clean pull must not report.
	f2 := cloudprov.NewFake()
	f2.SetCaps(cloudprov.Capabilities{APIVersion: "1.0", Billing: true})
	f2.Seed(cloudprov.Resource{ResourceID: "vm-a1", State: "running", BwMbps: 100})
	svc2 := New(db, fakeSecrets{}, func(storage.Provider, string) (cloudprov.Adapter, error) { return f2, nil })
	run, err = svc2.ImportFromAPI(context.Background(), ImportRequest{
		CostProviderID: src.ID, Window: cloudprov.BillingWindow{From: time.Now().Add(-24 * time.Hour)},
	}, "tester", time.Now())
	if err != nil || run.State != RunSucceeded {
		t.Fatalf("clean pull: run=%+v err=%v", run, err)
	}
	if len(got) != 1 {
		t.Fatalf("clean pull reported: %+v", got)
	}
}

// TestBillingSyncFailedAlert covers F-1830: a failed pull lands one
// billing_sync_failed warning per source per day; a second same-day failure
// stays quiet (the day key is the dedup); a successful pull never alerts.
func TestBillingSyncFailedAlert(t *testing.T) {
	db := openBillingDB(t)
	src := storage.CostProvider{Name: "syncsrc", Source: SourceAPI, Currency: "USD"}
	seedSource(t, db, &src)
	prov := storage.Provider{Name: "prov10", Type: "fake", AccessKey: "sealed", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	db.Model(&storage.CostProvider{}).Where("id = ?", src.ID).Update("provider_id", prov.ID)

	f := cloudprov.NewFake()
	f.SetCaps(cloudprov.Capabilities{APIVersion: "1.0", Billing: true})
	f.SetFailHook(cloudprov.OpImportBilling, func() *cloudprov.Error {
		return cloudprov.NewError(cloudprov.ErrRateLimited, cloudprov.OpImportBilling, "", "429", "quota")
	})
	svc := New(db, fakeSecrets{}, func(storage.Provider, string) (cloudprov.Adapter, error) { return f, nil })
	ctx := context.Background()
	req := ImportRequest{CostProviderID: src.ID, Window: cloudprov.BillingWindow{From: time.Now().Add(-24 * time.Hour)}}

	if run, err := svc.ImportFromAPI(ctx, req, "tester", time.Now()); err == nil || run.State != RunFailed {
		t.Fatalf("pull must fail: run=%+v err=%v", run, err)
	}
	var ev storage.Event
	if err := db.Where("kind = ?", herald.KindBillingSyncFailed).First(&ev).Error; err != nil {
		t.Fatalf("billing_sync_failed event missing: %v", err)
	}
	if ev.Target != herald.TargetAdmin || ev.Severity != herald.SeverityWarning {
		t.Fatalf("event shape: %+v", ev)
	}
	if !strings.HasPrefix(ev.DedupKey, fmt.Sprintf("billing_sync:%d:failed:", src.ID)) ||
		len(ev.DedupKey) != len(fmt.Sprintf("billing_sync:%d:failed:", src.ID))+8 {
		t.Fatalf("dedup key = %q", ev.DedupKey)
	}

	// A second same-day failure stays quiet.
	if _, err := svc.ImportFromAPI(ctx, req, "tester", time.Now()); err == nil {
		t.Fatal("second pull must fail too")
	}
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindBillingSyncFailed).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("same-day failures must dedup: %d events", n)
	}

	// A clean pull never alerts (a fresh fake: SetFailHook(op, nil) would
	// panic on the next failing call of that op).
	f3 := cloudprov.NewFake()
	f3.SetCaps(cloudprov.Capabilities{APIVersion: "1.0", Billing: true})
	f3.Seed(cloudprov.Resource{ResourceID: "vm-s1", State: "running", BwMbps: 100})
	svc3 := New(db, fakeSecrets{}, func(storage.Provider, string) (cloudprov.Adapter, error) { return f3, nil })
	if run, err := svc3.ImportFromAPI(ctx, req, "tester", time.Now()); err != nil || run.State != RunSucceeded {
		t.Fatalf("clean pull: run=%+v err=%v", run, err)
	}
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindBillingSyncFailed).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("success must stay quiet: %d events", n)
	}
}
