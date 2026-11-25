package handler

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newTestRouterWithMasterKey builds a test router with a secret master key (for
// OS-1 credential entry), returning a separate store with the same master key to
// verify the ciphertext.
func newTestRouterWithMasterKey(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	gin.SetMode(gin.TestMode)
	cfg := config.Default()
	cfg.SecretKey = "test-master-key"
	r, _ := NewRouter(db, cfg)
	return r, db
}

// TestProviderAPI covers provider credentials (OS-1): entry is encrypted before
// hitting the database (no plaintext stored), the list does not echo the key
// (has_access_key), secret rotation, an empty secret being preserved, providers
// with templates refusing deletion, and instances without a master key refusing
// credential entry.
func TestProviderAPI(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	store := secret.NewStore("test-master-key")

	// enter credentials
	rec := doJSON(t, r, "POST", "/api/providers", map[string]any{
		"name": "vultr-main", "type": "vultr", "access_key": "sk-live-secret",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create provider status = %d body=%s", rec.Code, rec.Body)
	}
	var pv struct {
		ID           uint   `json:"id"`
		HasAccessKey bool   `json:"has_access_key"`
		AccessKey    string `json:"access_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil {
		t.Fatal(err)
	}
	if !pv.HasAccessKey || pv.AccessKey != "" {
		t.Fatalf("provider view leaks key: %+v", pv)
	}

	// stored encrypted and decryptable back to plaintext
	var row storage.Provider
	if err := db.First(&row, pv.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.AccessKey == "sk-live-secret" {
		t.Fatal("access key must be stored encrypted")
	}
	if plain, err := store.Decrypt(row.AccessKey); err != nil || plain != "sk-live-secret" {
		t.Fatalf("decrypt stored key = %q err=%v", plain, err)
	}

	// list masking: only has_access_key, no plaintext
	rec = doJSON(t, r, "GET", "/api/providers", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	if !json.Valid([]byte(body)) || !containsStr(body, `"has_access_key":true`) || containsStr(body, "sk-live-secret") {
		t.Fatalf("list leaks key: %s", body)
	}

	// rotate the secret
	id := strconv.FormatUint(uint64(pv.ID), 10)
	rec = doJSON(t, r, "PUT", "/api/providers/"+id, map[string]any{"access_key": "sk-new"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update provider status = %d", rec.Code)
	}
	db.First(&row, pv.ID)
	if plain, err := store.Decrypt(row.AccessKey); err != nil || plain != "sk-new" {
		t.Fatalf("rotated key = %q err=%v", plain, err)
	}

	// empty secret preserved: rename only
	rec = doJSON(t, r, "PUT", "/api/providers/"+id, map[string]any{"name": "vultr-renamed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d", rec.Code)
	}
	db.First(&row, pv.ID)
	if row.Name != "vultr-renamed" {
		t.Fatalf("name = %s", row.Name)
	}
	if plain, err := store.Decrypt(row.AccessKey); err != nil || plain != "sk-new" {
		t.Fatal("rename must keep access key")
	}

	// deletion refused while a template references it
	rec = doJSON(t, r, "POST", "/api/provision-templates", map[string]any{
		"name": "hk-3t", "provider_id": pv.ID, "plan": "vc2-1c-1gb", "region": "hkg",
		"bw_mbps": 500, "billing_type": "包月", "direction": "out", "line_type": "163", "role": "entry",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create template status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "DELETE", "/api/providers/"+id, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete provider with templates status = %d", rec.Code)
	}

	// an instance without a master key refuses entry (no plaintext in the DB)
	r2, _ := newTestRouterWithDB(t)
	rec = doJSON(t, r2, "POST", "/api/providers", map[string]any{
		"name": "x", "type": "vultr", "access_key": "sk",
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("create without master key status = %d body=%s", rec.Code, rec.Body)
	}
}

// TestTemplateAPI covers machine templates (OS-1): CRUD and field validation.
func TestTemplateAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: "v1:a:b", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}

	// invalid direction refused
	rec := doJSON(t, r, "POST", "/api/provision-templates", map[string]any{
		"name": "bad", "provider_id": prov.ID, "direction": "sideways",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid direction status = %d", rec.Code)
	}
	// missing provider refused
	rec = doJSON(t, r, "POST", "/api/provision-templates", map[string]any{"name": "bad"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing provider status = %d", rec.Code)
	}
	// invalid JSON in the config template refused (OS-4 lands a node row per
	// provisioned server at open; an invalid config would be pointless)
	rec = doJSON(t, r, "POST", "/api/provision-templates", map[string]any{
		"name": "bad", "provider_id": prov.ID, "config": "{inbounds:",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid config status = %d", rec.Code)
	}

	// create a template (defaults direction=out role=entry; the config template
	// is stored compacted)
	rec = doJSON(t, r, "POST", "/api/provision-templates", map[string]any{
		"name": "hk-3t", "provider_id": prov.ID, "plan": "vc2-1c-1gb", "region": "hkg",
		"bw_mbps": 500, "billing_type": "包月", "monthly_cost_cents": 500,
		"traffic_price_cents": 0, "line_type": "163", "role": "entry", "transport": "ws-tls",
		"config": `{ "inbounds": [] }`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create template status = %d body=%s", rec.Code, rec.Body)
	}
	var tpl struct {
		ID               uint   `json:"id"`
		Name             string `json:"name"`
		Direction        string `json:"direction"`
		Role             string `json:"role"`
		BillingType      string `json:"billing_type"`
		MonthlyCostCents int64  `json:"monthly_cost_cents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tpl); err != nil {
		t.Fatal(err)
	}
	if tpl.Direction != "out" || tpl.Role != "entry" || tpl.BillingType != "包月" || tpl.MonthlyCostCents != 500 {
		t.Fatalf("template = %+v", tpl)
	}
	var row storage.ProvisionTemplate
	if err := db.First(&row, tpl.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Config != `{"inbounds":[]}` {
		t.Fatalf("template config must be compacted: %q", row.Config)
	}

	// update and delete
	tid := strconv.FormatUint(uint64(tpl.ID), 10)
	rec = doJSON(t, r, "PUT", "/api/provision-templates/"+tid, map[string]any{
		"name": "hk-3t-v2", "provider_id": prov.ID, "plan": "vc2-2c-2gb", "region": "hkg",
		"billing_type": "按流量", "traffic_price_cents": 30, "direction": "in", "line_type": "cn2_gia", "role": "both",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update template status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/provision-templates", nil)
	if rec.Code != http.StatusOK || !containsStr(rec.Body.String(), "hk-3t-v2") {
		t.Fatalf("list templates = %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "DELETE", "/api/provision-templates/"+tid, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete template status = %d", rec.Code)
	}
	var n int64
	db.Model(&storage.ProvisionTemplate{}).Count(&n)
	if n != 0 {
		t.Fatalf("templates after delete = %d", n)
	}
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
