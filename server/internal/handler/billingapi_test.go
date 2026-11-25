package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// TestBillingAPI covers the actual-bill surface (§4.1~§4.5): source CRUD, JSON
// file import with dedupe, bill/item listing, allocation and reconciliation.
func TestBillingAPI(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "billingapi.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	cfg := config.Default()
	cfg.SecretKey = "test-master-key"
	gin.SetMode(gin.TestMode)
	r, _ := NewRouter(db, cfg)

	// Source CRUD: a manual source needs name/source/currency (F-0502).
	rec := doJSON(t, r, http.MethodPost, "/api/cost/sources", map[string]any{
		"name": "vendor-a", "source": "manual", "currency": "USD",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":1`) {
		t.Fatalf("create source: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPost, "/api/cost/sources", map[string]any{
		"name": "bad", "source": "carrier-pigeon", "currency": "USD",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid source must fail: code=%d", rec.Code)
	}
	rec = doJSON(t, r, http.MethodPut, "/api/cost/sources/1", map[string]any{"note": "ops"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"note":"ops"`) {
		t.Fatalf("update source: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// File import (F-0523): two line items of ONE bill land as one record; a
	// re-import is deduped (F-0529) and every row keeps its raw snapshot
	// (F-0511).
	bill := `[
		{"bill_id":"INV-10","item_id":"l1","resource_id":"vm-x","category":"instance","amount_cents":900,"currency":"USD"},
		{"bill_id":"INV-10","item_id":"l2","resource_id":"vm-x","category":"traffic","amount_cents":100,"currency":"USD"}
	]`
	body := "------X\r\nContent-Disposition: form-data; name=\"file\"; filename=\"bill.json\"\r\nContent-Type: application/json\r\n\r\n" + bill + "\r\n------X--\r\n"
	rec = doJSONRaw(t, r, http.MethodPost, "/api/cost/sources/1/import-file", body, "multipart/form-data; boundary=----X")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("file import: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"added":1`) {
		t.Fatalf("import must add one record: %s", rec.Body.String())
	}
	rec = doJSONRaw(t, r, http.MethodPost, "/api/cost/sources/1/import-file", body, "multipart/form-data; boundary=----X")
	if !strings.Contains(rec.Body.String(), `"skipped":2`) {
		t.Fatalf("reimport must dedupe: %s", rec.Body.String())
	}
	// The run log records the run and its state (F-0532/F-0533).
	rec = doJSON(t, r, http.MethodGet, "/api/cost/imports", nil)
	if !strings.Contains(rec.Body.String(), `"state":"succeeded"`) {
		t.Fatalf("imports: %s", rec.Body.String())
	}
	// Bill records and their items are listed with filters (F-0636).
	rec = doJSON(t, r, http.MethodGet, "/api/cost/records", nil)
	if !strings.Contains(rec.Body.String(), `"bill_id":"INV-10"`) {
		t.Fatalf("records: %s", rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/cost/records?source=file", nil)
	if !strings.Contains(rec.Body.String(), "INV-10") {
		t.Fatalf("source filter: %s", rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/cost/records/1/items", nil)
	if !strings.Contains(rec.Body.String(), `"category":"instance"`) {
		t.Fatalf("items: %s", rec.Body.String())
	}

	// Allocation (F-0563/F-0566/F-0570): a fixed 60/40 rule over the traffic
	// item; a second run under the same version is a no-op.
	rec = doJSON(t, r, http.MethodPost, "/api/cost/allocation-rules", map[string]any{
		"name": "split", "kind": "ratio",
		"params": `{"targets":[{"kind":"node","key":"1"},{"kind":"node","key":"2"}],"ratios":[600000,400000]}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create rule: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodPost, "/api/cost/items/2/allocate", map[string]any{"rule_id": 1, "version": 1})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"allocated_cents":60`) {
		t.Fatalf("allocate: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodPost, "/api/cost/items/2/allocate", map[string]any{"rule_id": 1, "version": 1})
	if !strings.Contains(rec.Body.String(), `"allocations":[]`) {
		t.Fatalf("double allocation must be blocked: %s", rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/cost/allocations", nil)
	if !strings.Contains(rec.Body.String(), `"original_cents":100`) {
		t.Fatalf("allocations keep the original (F-0569): %s", rec.Body.String())
	}

	// Estimate (F-0550/F-0551/F-0552): stored separately, with its price
	// source; an unknown-price line is marked, never invented.
	rec = doJSON(t, r, http.MethodPost, "/api/cost/estimates", map[string]any{
		"kind": "instance", "amount_cents": 800, "currency": "USD",
		"price_source": "hand-2026-10", "period_start": "2026-10-01T00:00:00Z", "period_end": "2026-10-31T00:00:00Z",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"amount_cents":800`) {
		t.Fatalf("estimate: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodPost, "/api/cost/estimates", map[string]any{
		"kind": "snapshot", "currency": "USD", "unknown_price": true,
		"period_start": "2026-10-01T00:00:00Z", "period_end": "2026-10-31T00:00:00Z",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"unknown_price":true`) {
		t.Fatalf("unknown-price estimate: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Every estimate category is recordable (F-0542~F-0549): hourly, fixed
	// bandwidth, extra IP, disk, snapshot, backup, discount, custom item.
	for _, kind := range []string{"hourly", "bandwidth", "ip", "disk", "snapshot", "backup", "discount", "custom"} {
		rec = doJSON(t, r, http.MethodPost, "/api/cost/estimates", map[string]any{
			"kind": kind, "amount_cents": 10, "currency": "USD",
			"price_source": "hand-2026-10", "period_start": "2026-10-01T00:00:00Z", "period_end": "2026-10-31T00:00:00Z",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("estimate kind %s: code=%d body=%s", kind, rec.Code, rec.Body.String())
		}
	}
	rec = doJSON(t, r, http.MethodPost, "/api/cost/estimates", map[string]any{
		"kind": "disk", "currency": "USD",
		"period_start": "2026-10-01T00:00:00Z", "period_end": "2026-10-31T00:00:00Z",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("estimate without price source must fail: code=%d", rec.Code)
	}

	// Reconciliation (F-0587/F-0591): actual 1000 vs estimate 800, diff +200,
	// then confirmed with a note.
	rec = doJSON(t, r, http.MethodPost, "/api/cost/reconciliations", map[string]any{
		"scope_kind": "provider", "target_id": 1, "currency": "USD",
		"from": "2026-10-01T00:00:00Z", "to": "2026-10-31T00:00:00Z",
		"actual_cents": 1000, "estimate_cents": 800,
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"diff_cents":200`) {
		t.Fatalf("reconcile: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodPost, "/api/cost/reconciliations/1/confirm", map[string]any{
		"status": "confirmed", "note": "vendor rounding",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"confirmed"`) {
		t.Fatalf("confirm: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/cost/reconciliations", nil)
	if !strings.Contains(rec.Body.String(), `"diff_cents":200`) {
		t.Fatalf("list reconciliations: %s", rec.Body.String())
	}
}

// TestBillingAPIImportFromAPI covers the API pull path (F-0522/F-0524): the
// capability gate fails closed, and a billing-capable vendor imports records.
func TestBillingAPIImportFromAPI(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "billingapi2.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	f := cloudprov.NewFake()
	f.Seed(cloudprov.Resource{ResourceID: "vm-b", State: cloudprov.StateRunning, BwMbps: 100})
	cloudprov.Register("fake-billing-api", func(cloudprov.Account) (cloudprov.Adapter, error) { return f, nil })
	t.Cleanup(func() { cloudprov.Unregister("fake-billing-api") })

	sealed, _ := secret.NewStore("test-master-key").Encrypt("sk-billing-api")
	prov := storage.Provider{Name: "fb", Type: "fake-billing-api", AccessKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	cfg := config.Default()
	cfg.SecretKey = "test-master-key"
	gin.SetMode(gin.TestMode)
	r, _ := NewRouter(db, cfg)

	rec := doJSON(t, r, http.MethodPost, "/api/cost/sources", map[string]any{
		"name": "fb", "source": "api", "currency": "USD", "provider_id": prov.ID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create api source: code=%d body=%s", rec.Code, rec.Body.String())
	}
	from := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	rec = doJSON(t, r, http.MethodPost, "/api/cost/sources/1/import-api", map[string]any{"from": from})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("api import: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"added":1`) {
		t.Fatalf("api import must add one bill: %s", rec.Body.String())
	}
	// The record carries the vendor bill id and the item binds the resource.
	rec = doJSON(t, r, http.MethodGet, "/api/cost/records", nil)
	if !strings.Contains(rec.Body.String(), `"bill_id":"vm-b"`) {
		t.Fatalf("api records: %s", rec.Body.String())
	}
	// A source without a billing capability fails closed (F-0522).
	f.SetCaps(cloudprov.Capabilities{APIVersion: "1.0"})
	rec = doJSON(t, r, http.MethodPost, "/api/cost/sources/1/import-api", map[string]any{"from": from})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("no-billing capability must fail closed: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestBillingAPICostLines covers the cost-line CRUD and the estimate compute
// endpoint (F-0542~F-0549): lines feed the estimate engine through the API.
func TestBillingAPICostLines(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "billingapi3.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	node := storage.Node{Name: "cl", Token: "tk-cl", BillingType: "包月", MonthlyCostCents: 3100, Currency: "CNY"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	cfg := config.Default()
	cfg.SecretKey = "test-master-key"
	gin.SetMode(gin.TestMode)
	r, _ := NewRouter(db, cfg)

	// Hourly line (F-0542).
	rec := doJSON(t, r, http.MethodPost, "/api/cost/lines", map[string]any{
		"node_id": node.ID, "kind": "hourly", "hourly_price_cents": 10, "note": "spot compute",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"kind":"hourly"`) {
		t.Fatalf("create hourly line: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Extra IP and discount lines (F-0545/F-0548); a negative non-discount fails.
	rec = doJSON(t, r, http.MethodPost, "/api/cost/lines", map[string]any{
		"node_id": node.ID, "kind": "ip", "amount_cents": 310, "currency": "CNY",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create ip line: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodPost, "/api/cost/lines", map[string]any{
		"node_id": node.ID, "kind": "discount", "amount_cents": -3100,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create discount line: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodPost, "/api/cost/lines", map[string]any{
		"node_id": node.ID, "kind": "ip", "amount_cents": -5,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative non-discount must fail: code=%d", rec.Code)
	}
	rec = doJSON(t, r, http.MethodPost, "/api/cost/lines", map[string]any{
		"node_id": node.ID, "kind": "carrier-pigeon", "amount_cents": 5,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid kind must fail: code=%d", rec.Code)
	}

	// Compute over a half-month period: instance and lines prorate, hourly
	// computes price x hours (F-0542).
	rec = doJSON(t, r, http.MethodPost, "/api/cost/estimates/compute", map[string]any{
		"node_id": node.ID, "from": "2026-10-01T00:00:00Z", "to": "2026-10-16T00:00:00Z",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"count":4`) {
		t.Fatalf("compute: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"amount_cents":3600`) {
		t.Fatalf("hourly estimate must be 10x360h: %s", rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/cost/estimates?node_id="+fmt.Sprint(node.ID), nil)
	for _, kind := range []string{"instance", "hourly", "ip", "discount"} {
		if !strings.Contains(rec.Body.String(), `"`+kind+`"`) {
			t.Fatalf("estimates must list %s: %s", kind, rec.Body.String())
		}
	}
	// Bulk compute path.
	rec = doJSON(t, r, http.MethodPost, "/api/cost/estimates/compute", map[string]any{
		"from": "2026-10-01T00:00:00Z", "to": "2026-11-01T00:00:00Z",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"count":4`) {
		t.Fatalf("bulk compute: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Line edit and delete (F-0567-adjacent manual adjustment surface).
	rec = doJSON(t, r, http.MethodPut, "/api/cost/lines/2", map[string]any{"amount_cents": 620})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"amount_cents":620`) {
		t.Fatalf("update line: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodDelete, "/api/cost/lines/2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete line: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/cost/lines?node_id="+fmt.Sprint(node.ID), nil)
	if strings.Contains(rec.Body.String(), `"amount_cents":620`) {
		t.Fatalf("deleted line must be gone: %s", rec.Body.String())
	}
}

// TestBillingAPISpend covers the aggregate endpoints (F-0581~F-0583/F-0593):
// per-provider and per-account actual sums plus the month trend, each
// per-currency (F-0594).
func TestBillingAPISpend(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "billingapi4.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	src := storage.CostProvider{Name: "vendor-b", Source: "manual", Currency: "USD"}
	if err := db.Create(&src).Error; err != nil {
		t.Fatalf("source: %v", err)
	}
	day := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	rec := storage.CostRecord{
		CostProviderID: src.ID, Source: "manual", BillID: "SP-1", Account: "acc-x", Region: "eu-1",
		Currency: "USD", AmountCents: 700, IncurredAt: day, PeriodStart: day, PeriodEnd: day.AddDate(0, 1, 0),
		Status: "settled", DedupeKey: "manual:1:SP-1:0",
	}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("record: %v", err)
	}
	cfg := config.Default()
	cfg.SecretKey = "test-master-key"
	gin.SetMode(gin.TestMode)
	r, _ := NewRouter(db, cfg)

	rec2 := doJSON(t, r, http.MethodGet, "/api/cost/spend?scope=provider", nil)
	if rec2.Code != http.StatusOK || !strings.Contains(rec2.Body.String(), `"actual_cents":700`) {
		t.Fatalf("provider spend: code=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), `"name":"vendor-b"`) {
		t.Fatalf("provider spend must name the source: %s", rec2.Body.String())
	}
	rec2 = doJSON(t, r, http.MethodGet, "/api/cost/spend?scope=account", nil)
	if !strings.Contains(rec2.Body.String(), `"key":"acc-x"`) {
		t.Fatalf("account spend: %s", rec2.Body.String())
	}
	rec2 = doJSON(t, r, http.MethodGet, "/api/cost/spend?scope=galaxy", nil)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("invalid scope must fail: code=%d", rec2.Code)
	}
	rec2 = doJSON(t, r, http.MethodGet, "/api/cost/spend/trend?bucket=month", nil)
	if rec2.Code != http.StatusOK || !strings.Contains(rec2.Body.String(), `"bucket":"2026-10"`) {
		t.Fatalf("month trend: code=%d body=%s", rec2.Code, rec2.Body.String())
	}
	rec2 = doJSON(t, r, http.MethodGet, "/api/cost/spend/trend?bucket=week", nil)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("invalid bucket must fail: code=%d", rec2.Code)
	}
	// Anomaly review (F-0588~F-0590): from is required, growth_pct parsed.
	rec2 = doJSON(t, r, http.MethodGet, "/api/cost/anomalies", nil)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("anomalies without from must fail: code=%d", rec2.Code)
	}
	rec2 = doJSON(t, r, http.MethodGet, "/api/cost/anomalies?from=2026-10-01T00:00:00Z&to=2026-11-30T00:00:00Z", nil)
	if rec2.Code != http.StatusOK || !strings.Contains(rec2.Body.String(), `"anomalies"`) {
		t.Fatalf("anomalies: code=%d body=%s", rec2.Code, rec2.Body.String())
	}
}

// doJSONRaw posts a pre-encoded body with an explicit content type.
func doJSONRaw(t *testing.T, r *gin.Engine, method, path, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	// Same admin JWT contract as doJSON: the billing plane sits behind apiAuth.
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
