package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestDNSAPI covers the domain-front admin plane (BR-2): credential entry with
// encrypted storage, unknown types rejected (no claiming support), front record
// validation, switched-off fronts refused deletion, and providers with fronts
// refusing deletion.
func TestDNSAPI(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	store := secret.NewStore("test-master-key")

	// Unknown type rejected at entry.
	rec := doJSON(t, r, "POST", "/api/dns-providers", map[string]any{
		"name": "ali", "type": "alidns", "api_key": "k",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind status = %d body=%s", rec.Code, rec.Body)
	}

	// Credential entry: no plaintext in the DB; the list only reports has_api_key.
	rec = doJSON(t, r, "POST", "/api/dns-providers", map[string]any{
		"name": "cf-main", "type": "cloudflare", "api_key": "cf-token-secret",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body)
	}
	var pv struct {
		ID        uint `json:"id"`
		HasAPIKey bool `json:"has_api_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil {
		t.Fatal(err)
	}
	if !pv.HasAPIKey {
		t.Fatalf("view = %+v", pv)
	}
	var row storage.DNSProvider
	if err := db.First(&row, pv.ID).Error; err != nil || row.APIKey == "cf-token-secret" {
		t.Fatalf("key must be sealed: %+v err=%v", row, err)
	}
	if plain, err := store.Decrypt(row.APIKey); err != nil || plain != "cf-token-secret" {
		t.Fatalf("decrypt = %q err=%v", plain, err)
	}

	// Front record: non-JSON-array backup_ips is rejected.
	rec = doJSON(t, r, "POST", "/api/dns-fronts", map[string]any{
		"name": "主入口", "domain": "edge.example.com", "provider_id": pv.ID,
		"primary_ip": "10.0.0.1", "backup_ips": "10.0.0.2",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad backup_ips status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/dns-fronts", map[string]any{
		"name": "主入口", "domain": "edge.example.com", "provider_id": pv.ID,
		"primary_ip": "10.0.0.1", "backup_ips": `["10.0.0.2","10.0.0.3"]`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create front status = %d body=%s", rec.Code, rec.Body)
	}
	var fv storage.DNSFront
	if err := json.Unmarshal(rec.Body.Bytes(), &fv); err != nil {
		t.Fatal(err)
	}

	// Provider with fronts refuses deletion.
	rec = doJSON(t, r, "DELETE", "/api/dns-providers/"+strconv.FormatUint(uint64(pv.ID), 10), nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete provider with fronts status = %d", rec.Code)
	}

	// Switched-off front refused deletion (wait for failback / manual restore first).
	if err := db.Model(&storage.DNSFront{}).Where("id = ?", fv.ID).
		Update("switched", true).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "DELETE", "/api/dns-fronts/"+strconv.FormatUint(uint64(fv.ID), 10), nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete switched front status = %d", rec.Code)
	}
	if err := db.Model(&storage.DNSFront{}).Where("id = ?", fv.ID).
		Update("switched", false).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "DELETE", "/api/dns-fronts/"+strconv.FormatUint(uint64(fv.ID), 10), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete front status = %d body=%s", rec.Code, rec.Body)
	}
}

// TestGeoSync covers the geo reconciliation compensation entry (E-27): with no
// enabled DNS provider it is a no-op returning changed=0; fronts under a
// disabled provider do not join reconciliation (channel behavior is covered by
// the geodns package's fake channel, no real callout here).
func TestGeoSync(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)

	rec := doJSON(t, r, "POST", "/api/dns-fronts/geo-sync", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("no provider status = %d body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Changed int `json:"changed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Changed != 0 {
		t.Fatalf("body=%s err=%v", rec.Body, err)
	}

	// Disabled provider plus its front: not in reconciliation, still a no-op.
	store := secret.NewStore("test-master-key")
	sealed, err := store.Encrypt("tok-x")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.DNSProvider{Name: "cf-off", Type: "cloudflare", APIKey: sealed, Enabled: false}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	front := storage.DNSFront{Name: "主入口", Domain: "edge.example.com", ProviderID: prov.ID,
		PrimaryIP: "10.0.0.1", BackupIPs: `["10.0.0.2"]`}
	if err := db.Create(&front).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "POST", "/api/dns-fronts/geo-sync", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled provider status = %d body=%s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Changed != 0 {
		t.Fatalf("body=%s err=%v", rec.Body, err)
	}
}
