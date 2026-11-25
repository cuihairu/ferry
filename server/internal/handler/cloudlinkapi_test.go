package handler

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/pool"
	// placeholder
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

func TestCloudLinkAPI(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "cloudlinkapi.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	sealed, _ := secret.NewStore("test-master-key").Encrypt("sk-cloudlinkapi")
	p := storage.Provider{Name: "link-api", Type: "fake-link-api", AccessKey: sealed, Enabled: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	node := storage.Node{
		Name: "vm-link-api", Token: "tok-link-api", Port: 443, Protocol: "vless",
		Enabled: true, Status: "online", PoolState: pool.StateActive,
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	res := storage.CloudResource{
		ProviderID: p.ID, ExternalID: "vm-link-api", Region: "fra1",
		State: cloudprov.StateRunning, NodeID: &node.ID,
	}
	if err := db.Create(&res).Error; err != nil {
		t.Fatalf("resource: %v", err)
	}
	f := cloudprov.NewFake()
	f.Seed(cloudprov.Resource{ResourceID: "vm-link-api", State: cloudprov.StateStopped})
	cloudprov.Register("fake-link-api", func(cloudprov.Account) (cloudprov.Adapter, error) { return f, nil })
	t.Cleanup(func() { cloudprov.Unregister("fake-link-api") })

	cfg := config.Default()
	cfg.SecretKey = "test-master-key"
	gin.SetMode(gin.TestMode)
	r, _ := NewRouter(db, cfg)

	// Controlled link applies the real vendor state to the bound Node.
	rec := doJSON(t, r, http.MethodPost, "/api/cloud-resources/"+itoa(res.ID)+"/link", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"changed":true`) {
		t.Fatalf("link: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var after storage.Node
	if err := db.First(&after, node.ID).Error; err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if !after.Maintenance || after.PoolState != pool.StateSuspended {
		t.Fatalf("node after link = %+v", after)
	}
	// An unbound resource is a 409, and a missing resource is a 404.
	unbound := storage.CloudResource{ProviderID: p.ID, ExternalID: "unbound", State: cloudprov.StateRunning}
	if err := db.Create(&unbound).Error; err != nil {
		t.Fatalf("unbound: %v", err)
	}
	if rec := doJSON(t, r, http.MethodPost, "/api/cloud-resources/"+itoa(unbound.ID)+"/link", nil); rec.Code != http.StatusConflict {
		t.Fatalf("unbound link code = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, r, http.MethodPost, "/api/cloud-resources/999999/link", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing link code = %d, want 404: %s", rec.Code, rec.Body.String())
	}

	// Pre-shutdown risk, drain and post-boot checks are exposed on the Node.
	if err := db.Create(&storage.NodeTrafficLog{
		NodeID: node.ID, Proc: "relay", RxBytes: 4096, RecordedAt: time.Now().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("traffic: %v", err)
	}
	rec = doJSON(t, r, http.MethodGet, "/api/nodes/"+itoa(node.ID)+"/cloud-shutdown-check", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"risk":true`) {
		t.Fatalf("shutdown check: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodPost, "/api/nodes/"+itoa(node.ID)+"/cloud-drain", map[string]any{"reason": "maintenance"})
	if rec.Code != http.StatusOK {
		t.Fatalf("drain: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := db.First(&after, node.ID).Error; err != nil {
		t.Fatalf("reload drained node: %v", err)
	}
	if !after.Maintenance || after.PoolState != pool.StateStandby {
		t.Fatalf("node after drain = %+v", after)
	}
	// A boot without Agent checks fails the health verdict and emits an alert.
	rec = doJSON(t, r, http.MethodPost, "/api/nodes/"+itoa(node.ID)+"/cloud-boot-check", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"healthy":false`) {
		t.Fatalf("boot check: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var events int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindCloudBootFailed).Count(&events).Error; err != nil || events != 1 {
		t.Fatalf("boot alert events = %d err=%v", events, err)
	}
	// Path health uses the same pathhealth verdict as the other surfaces.
	rec = doJSON(t, r, http.MethodGet, "/api/nodes/"+itoa(node.ID)+"/cloud-path-health", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"paths"`) {
		t.Fatalf("path health: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Each linkage decision wrote an audit row.
	var audits int64
	if err := db.Model(&storage.AuditLog{}).Where("action IN ?", []string{
		"cloud_node_link", "cloud_node_drain",
	}).Count(&audits).Error; err != nil || audits < 2 {
		t.Fatalf("audit rows = %d err=%v", audits, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode path health: %v", err)
	}
}
