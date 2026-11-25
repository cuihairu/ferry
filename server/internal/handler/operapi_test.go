package handler

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// TestCloudOperationAPI covers the task API surface (F-0361/F-0370/F-0375):
// create (queued), list/detail with step logs, high-risk confirmation 409,
// unknown-operation validation, and cancellation.
func TestCloudOperationAPI(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "opapi.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	sealed, _ := secret.NewStore("test-master-key").Encrypt("sk-opapi")
	p := storage.Provider{Name: "p", Type: "fake-opapi", AccessKey: sealed, Enabled: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	res := storage.CloudResource{ProviderID: p.ID, ExternalID: "vm-1", State: cloudprov.StateStopped}
	if err := db.Create(&res).Error; err != nil {
		t.Fatalf("resource: %v", err)
	}
	f := cloudprov.NewFake()
	f.Seed(cloudprov.Resource{ResourceID: "vm-1", State: cloudprov.StateStopped})
	cloudprov.Register("fake-opapi", func(cloudprov.Account) (cloudprov.Adapter, error) { return f, nil })
	t.Cleanup(func() { cloudprov.Unregister("fake-opapi") })

	cfg := config.Default()
	cfg.SecretKey = "test-master-key"
	gin.SetMode(gin.TestMode)
	r, _ := NewRouter(db, cfg)

	// High-risk without confirmation -> 409.
	rec := doJSON(t, r, "POST", "/api/cloud-operations", map[string]any{
		"provider_id": p.ID, "op": cloudprov.OpPowerOff,
		"specs": []map[string]any{{"resource_id": res.ID}},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("unconfirmed status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Unknown op -> 400.
	rec = doJSON(t, r, "POST", "/api/cloud-operations", map[string]any{
		"provider_id": p.ID, "op": "destroy",
		"specs": []map[string]any{{"resource_id": res.ID}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad op status=%d", rec.Code)
	}
	// Boot op succeeds and executes synchronously in the request path.
	rec = doJSON(t, r, "POST", "/api/cloud-operations", map[string]any{
		"provider_id": p.ID, "op": cloudprov.OpPowerOn,
		"specs":           []map[string]any{{"resource_id": res.ID}},
		"idempotency_key": "api-key-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	created := mustJSON(t, rec)
	if created["count"] != float64(1) {
		t.Fatalf("create body=%v", created)
	}
	ops, ok := created["operations"].([]any)
	if !ok || len(ops) != 1 {
		t.Fatalf("operations=%v", created)
	}
	op := ops[0].(map[string]any)
	id := uint(op["id"].(float64))
	if op["state"] != "succeeded" {
		t.Fatalf("created op=%v", op)
	}
	// Replay with the same key returns the same rows (idempotency).
	rec = doJSON(t, r, "POST", "/api/cloud-operations", map[string]any{
		"provider_id": p.ID, "op": cloudprov.OpPowerOn,
		"specs":           []map[string]any{{"resource_id": res.ID}},
		"idempotency_key": "api-key-1",
	})
	replay := mustJSON(t, rec)
	if replay["id"] != created["id"] {
		t.Fatalf("replay id=%v original=%v", replay["id"], created["id"])
	}
	// List/detail with logs.
	list := mustJSONList(t, doJSON(t, r, "GET", "/api/cloud-operations?resource_id="+utoa(res.ID), nil))
	if len(list) != 1 || list[0]["state"] != "succeeded" {
		t.Fatalf("list=%v", list)
	}
	detail := mustJSON(t, doJSON(t, r, "GET", "/api/cloud-operations/"+utoa(id), nil))
	logs, ok := detail["logs"].([]any)
	if !ok || len(logs) < 2 {
		t.Fatalf("detail logs=%v", detail)
	}
	if rec := doJSON(t, r, "GET", "/api/cloud-operations/99999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing detail status=%d", rec.Code)
	}
	// Terminal operations are not cancellable.
	if rec := doJSON(t, r, "POST", "/api/cloud-operations/"+utoa(id)+"/cancel", nil); rec.Code != http.StatusConflict {
		t.Fatalf("terminal cancel status=%d", rec.Code)
	}
	// Unknown resource -> 404.
	rec = doJSON(t, r, "POST", "/api/cloud-operations", map[string]any{
		"provider_id": p.ID, "op": cloudprov.OpPowerOn,
		"specs":           []map[string]any{{"resource_id": 99999}},
		"idempotency_key": "api-key-missing",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing resource status=%d body=%s", rec.Code, rec.Body.String())
	}
}
