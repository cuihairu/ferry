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

// 补齐管理端零覆盖口（十三批）：活动列表、DNS 凭证/前置更新、证书编排器
// 构造口径。

// TestListCampaigns 活动列表端：建两行按 id DESC 返回。
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

// TestUpdateDNSProvider 凭证更新端：改名/停用保留原 key、换 key 明文重封、
// 非法 id 与不存在行 404。
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

	// 保留原 key：改名换型并停用。
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

	// 换 key：明文重封落库。
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

// TestUpdateDNSFront 前置更新端：整包更新落库、坏备份 IP 拒、指向不存在
// 的凭证 400、非法 id 与不存在行 404。
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

// TestGetCertManager 证书编排器构造口径：bin/home/webroot 随配置、CA 固定
// letsencrypt。
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
