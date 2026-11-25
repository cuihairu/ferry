package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// Filling in the admin endpoints with zero coverage (thirteenth batch): campaign listing,
// DNS provider/front update, and the cert orchestrator construction.

// TestListCampaigns campaign listing endpoint: two rows created, returned in id DESC order.
func TestListCampaigns(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	now := time.Now()
	start := now.Add(-time.Hour).Format(time.RFC3339)
	end := now.Add(time.Hour).Format(time.RFC3339)
	for _, name := range []string{"c1", "c2"} {
		rec := doJSON(t, r, "POST", "/api/campaigns",
			mkCampaign(name, "timed", map[string]any{"kind": "cut", "value": 100}, start, end))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body)
		}
	}

	rec := doJSON(t, r, "GET", "/api/campaigns", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var rows []storage.Campaign
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Name != "c2" {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestUpdateDNSProvider provider update endpoint: renaming/disabling keeps the original key, key
// rotation re-seals the plaintext, and an invalid id or missing row gives 404.
func TestUpdateDNSProvider(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	rec := doJSON(t, r, "POST", "/api/dns-providers", map[string]any{
		"name": "cf-main", "type": "cloudflare", "api_key": "old-secret",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var pv struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatUint(uint64(pv.ID), 10)

	// Keep the original key: rename, change type, and disable.
	rec = doJSON(t, r, "PUT", "/api/dns-providers/"+id, map[string]any{
		"name": "cf-alt", "type": "cloudflare", "enabled": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var view struct {
		Name      string `json:"name"`
		Enabled   bool   `json:"enabled"`
		HasAPIKey bool   `json:"has_api_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Name != "cf-alt" || view.Enabled || !view.HasAPIKey {
		t.Fatalf("view = %+v", view)
	}
	var row storage.DNSProvider
	if err := db.First(&row, pv.ID).Error; err != nil {
		t.Fatal(err)
	}
	store := secret.NewStore("test-master-key")
	if plain, err := store.Decrypt(row.APIKey); err != nil || plain != "old-secret" {
		t.Fatalf("key must be preserved: %q err=%v", plain, err)
	}

	// Replace the key: the plaintext is re-sealed and persisted.
	rec = doJSON(t, r, "PUT", "/api/dns-providers/"+id, map[string]any{
		"name": "cf-alt", "type": "cloudflare", "api_key": "new-secret",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&row, pv.ID).Error; err != nil {
		t.Fatal(err)
	}
	if plain, err := store.Decrypt(row.APIKey); err != nil || plain != "new-secret" {
		t.Fatalf("key must rotate: %q err=%v", plain, err)
	}

	if rec = doJSON(t, r, "PUT", "/api/dns-providers/abc", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}
	if rec = doJSON(t, r, "PUT", "/api/dns-providers/99999", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing row: %d", rec.Code)
	}
}

// TestUpdateDNSFront front update endpoint: a full-package update is persisted, a bad backup IP is
// rejected, a nonexistent provider gives 400, and an invalid id or missing row gives 404.
func TestUpdateDNSFront(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	store := secret.NewStore("test-master-key")
	sealed, err := store.Encrypt("tok-x")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.DNSProvider{Name: "cf-main", Type: "cloudflare", APIKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	front := storage.DNSFront{Name: "主入口", Domain: "edge.example.com", ProviderID: prov.ID,
		PrimaryIP: "10.0.0.1", BackupIPs: `["10.0.0.2"]`}
	if err := db.Create(&front).Error; err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatUint(uint64(front.ID), 10)

	rec := doJSON(t, r, "PUT", "/api/dns-fronts/"+id, map[string]any{
		"name": "主入口改", "domain": "edge2.example.com", "provider_id": prov.ID,
		"primary_ip": "10.0.0.9", "backup_ips": `["10.0.0.8"]`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var row storage.DNSFront
	if err := db.First(&row, front.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Name != "主入口改" || row.Domain != "edge2.example.com" || row.PrimaryIP != "10.0.0.9" || row.BackupIPs != `["10.0.0.8"]` {
		t.Fatalf("row = %+v", row)
	}

	if rec = doJSON(t, r, "PUT", "/api/dns-fronts/"+id, map[string]any{
		"name": "x", "domain": "edge2.example.com", "provider_id": prov.ID,
		"primary_ip": "10.0.0.9", "backup_ips": "bad",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad ips: %d %s", rec.Code, rec.Body)
	}
	missing := uint(99999)
	if rec = doJSON(t, r, "PUT", "/api/dns-fronts/"+id, map[string]any{
		"name": "x", "domain": "edge2.example.com", "provider_id": missing,
		"primary_ip": "10.0.0.9", "backup_ips": `["10.0.0.8"]`,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing provider: %d", rec.Code)
	}
	if rec = doJSON(t, r, "PUT", "/api/dns-fronts/abc", map[string]any{}); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}
	if rec = doJSON(t, r, "PUT", "/api/dns-fronts/99999", map[string]any{
		"name": "x", "domain": "edge2.example.com", "provider_id": prov.ID,
		"primary_ip": "10.0.0.9", "backup_ips": `["10.0.0.8"]`,
	}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing row: %d", rec.Code)
	}
}

// TestGetCertManager cert orchestrator construction: bin/home/webroot follow the config, CA is
// pinned to letsencrypt.
func TestGetCertManager(t *testing.T) {
	cfg := config.Default()
	cfg.AcmeBin = "/usr/bin/acme.sh"
	cfg.AcmeHome = "/var/lib/acme"
	cfg.AcmeWebroot = "/var/www/acme"
	h := &Handler{cfg: cfg}
	m := h.getCertManager()
	if m.Bin != cfg.AcmeBin || m.Home != cfg.AcmeHome || m.Webroot != cfg.AcmeWebroot {
		t.Fatalf("manager = %+v; cfg = %+v", m, cfg)
	}
	if m.CA != "letsencrypt" {
		t.Fatalf("CA = %q; want letsencrypt", m.CA)
	}
}
