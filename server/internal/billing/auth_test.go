package billing

import (
	"context"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
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
