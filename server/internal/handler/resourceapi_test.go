package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// mustJSONList decodes a JSON array response into generic objects.
func mustJSONList(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list: %v body=%s", err, rec.Body.String())
	}
	return out
}

func cloudResourceTestDB(t *testing.T) (*gorm.DB, storage.Provider) {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "resources.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	sealed, err := secret.NewStore("test-master-key").Encrypt("sk-resource")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	p := storage.Provider{Name: "resource-provider", Type: "fake-resource", AccessKey: sealed, Enabled: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return db, p
}

func cloudResourceTestNode(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	n := storage.Node{Name: name, Port: 443, Token: "tok-" + name}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	return n.ID
}

// TestCloudResourceAPI covers F-0346/F-0353~F-0356: manual sync via the
// API, list/detail filtering (including tags), binding/unbinding, the
// one-Node-per-resource conflict and the sync history endpoint.
func TestCloudResourceAPI(t *testing.T) {
	db, p := cloudResourceTestDB(t)
	cfg := config.Default()
	cfg.SecretKey = "test-master-key"
	gin.SetMode(gin.TestMode)
	r, _ := NewRouter(db, cfg)
	f := cloudprov.NewFake()
	f.Seed(cloudprov.Resource{ResourceID: "vm-1", Name: "alpha", Region: "fra1",
		State: cloudprov.StateRunning, PublicIP: "1.2.3.4", BwMbps: 50,
		Tags: map[string]string{"ferry": "managed"}})
	f.Seed(cloudprov.Resource{ResourceID: "vm-2", Name: "beta", Region: "nyc1",
		State: cloudprov.StateStopped})
	cloudprov.Register("fake-resource", func(cloudprov.Account) (cloudprov.Adapter, error) { return f, nil })
	t.Cleanup(func() { cloudprov.Unregister("fake-resource") })

	rec := doJSON(t, r, "POST", "/api/providers/"+utoa(p.ID)+"/sync", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := mustJSON(t, rec)
	if body["ok"] != true || body["added"] != float64(2) {
		t.Fatalf("sync body=%v", body)
	}
	list := mustJSONList(t, doJSON(t, r, "GET", "/api/cloud-resources", nil))
	if len(list) != 2 {
		t.Fatalf("list=%v", list)
	}
	filtered := mustJSONList(t, doJSON(t, r, "GET", "/api/cloud-resources?tag_key=ferry&tag_value=managed", nil))
	if len(filtered) != 1 || filtered[0]["external_id"] != "vm-1" {
		t.Fatalf("tag filter=%v", filtered)
	}
	filtered = mustJSONList(t, doJSON(t, r, "GET", "/api/cloud-resources?region=nyc1&state=stopped", nil))
	if len(filtered) != 1 || filtered[0]["external_id"] != "vm-2" {
		t.Fatalf("field filter=%v", filtered)
	}
	var resource storage.CloudResource
	if err := db.Where("external_id = ?", "vm-1").First(&resource).Error; err != nil {
		t.Fatalf("load resource: %v", err)
	}
	detail := mustJSON(t, doJSON(t, r, "GET", "/api/cloud-resources/"+utoa(resource.ID), nil))
	if detail["external_id"] != "vm-1" || detail["public_ip"] != "1.2.3.4" {
		t.Fatalf("detail=%v", detail)
	}
	n1 := cloudResourceTestNode(t, db, "node-1")
	n2 := cloudResourceTestNode(t, db, "node-2")
	if rec := doJSON(t, r, "POST", "/api/cloud-resources/"+utoa(resource.ID)+"/bind", map[string]any{"node_id": n1}); rec.Code != http.StatusOK {
		t.Fatalf("bind status=%d body=%s", rec.Code, rec.Body.String())
	}
	var bound storage.CloudResource
	if err := db.First(&bound, resource.ID).Error; err != nil || bound.NodeID == nil || *bound.NodeID != n1 {
		t.Fatalf("bound row=%+v err=%v", bound, err)
	}
	// A second resource cannot claim the same active Node (F-0355).
	var other storage.CloudResource
	if err := db.Where("external_id = ?", "vm-2").First(&other).Error; err != nil {
		t.Fatalf("load other: %v", err)
	}
	rec = doJSON(t, r, "POST", "/api/cloud-resources/"+utoa(other.ID)+"/bind", map[string]any{"node_id": n1})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate bind status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, "POST", "/api/cloud-resources/"+utoa(resource.ID)+"/bind", map[string]any{"node_id": n2}); rec.Code != http.StatusOK {
		t.Fatalf("rebind status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, "POST", "/api/cloud-resources/"+utoa(resource.ID)+"/unbind", nil); rec.Code != http.StatusOK {
		t.Fatalf("unbind status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := db.First(&bound, resource.ID).Error; err != nil || bound.NodeID != nil || bound.UnboundAt == nil {
		t.Fatalf("unbound row=%+v err=%v", bound, err)
	}
	if rec := doJSON(t, r, "POST", "/api/cloud-resources/"+utoa(resource.ID)+"/unbind", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("double unbind status=%d", rec.Code)
	}
	logs := mustJSONList(t, doJSON(t, r, "GET", "/api/cloud-sync-logs?provider_id="+utoa(p.ID), nil))
	if len(logs) != 1 || logs[0]["trigger"] != "manual" || logs[0]["ok"] != true {
		t.Fatalf("sync logs=%v", logs)
	}
}

// TestCloudResourceAPIValidation covers the fail-closed paths: unknown
// resources, missing/invalid binding input, unknown nodes, invalid filters,
// and a manual sync of a provider with no registered adapter.
func TestCloudResourceAPIValidation(t *testing.T) {
	db, p := cloudResourceTestDB(t)
	cfg := config.Default()
	cfg.SecretKey = "test-master-key"
	gin.SetMode(gin.TestMode)
	r, _ := NewRouter(db, cfg)

	if rec := doJSON(t, r, "GET", "/api/cloud-resources/999999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing detail status=%d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/cloud-resources?state=bogus", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state status=%d", rec.Code)
	}
	res := storage.CloudResource{ProviderID: p.ID, ExternalID: "vm-x", Region: "fra1", Name: "x", LastSeenAt: time.Now()}
	if err := db.Create(&res).Error; err != nil {
		t.Fatalf("create resource: %v", err)
	}
	base := "/api/cloud-resources/" + utoa(res.ID) + "/bind"
	if rec := doJSON(t, r, "POST", base, map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty bind status=%d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", base, map[string]any{"node_id": 4242}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown node bind status=%d", rec.Code)
	}
	rec := doJSON(t, r, "POST", "/api/providers/"+utoa(p.ID)+"/sync", map[string]any{})
	if rec.Code != http.StatusOK || mustJSON(t, rec)["ok"] != false {
		t.Fatalf("no-adapter sync status=%d body=%s", rec.Code, rec.Body.String())
	}
	logs := mustJSONList(t, doJSON(t, r, "GET", "/api/cloud-sync-logs", nil))
	if len(logs) != 1 || logs[0]["ok"] != false {
		t.Fatalf("failed sync log=%v", logs)
	}
}
