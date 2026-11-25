package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestNodeProviderASN covers F-0207: a node carries asn plus an optional
// provider link; the link is validated against existing providers, 0 clears
// it, and absent fields keep their values.
func TestNodeProviderASN(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: "k", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	prov2 := storage.Provider{Name: "hetzner", Type: "hetzner", AccessKey: "k", Enabled: true}
	if err := db.Create(&prov2).Error; err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "asn-node", "address": "asn.example.com", "port": 443,
		"protocol": "vless", "isp": "vultr", "asn": "AS64512", "provider_id": prov.ID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var n storage.Node
	if err := db.First(&n, uint(mustJSON(t, rec)["id"].(float64))).Error; err != nil {
		t.Fatal(err)
	}
	if n.ASN != "AS64512" || n.ProviderID == nil || *n.ProviderID != prov.ID {
		t.Fatalf("created row asn=%q provider=%v", n.ASN, n.ProviderID)
	}

	// Unknown provider is rejected.
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(n.ID), map[string]any{"provider_id": 99999}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown provider = %d %s", rec.Code, rec.Body)
	}

	// Switch to another provider, then clear with 0.
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(n.ID), map[string]any{"provider_id": prov2.ID}); rec.Code != http.StatusOK {
		t.Fatalf("switch provider: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&n, n.ID).Error; err != nil {
		t.Fatal(err)
	}
	if n.ProviderID == nil || *n.ProviderID != prov2.ID {
		t.Fatalf("after switch provider=%v", n.ProviderID)
	}
	zero := 0
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(n.ID), map[string]any{"provider_id": zero}); rec.Code != http.StatusOK {
		t.Fatalf("clear provider: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&n, n.ID).Error; err != nil {
		t.Fatal(err)
	}
	if n.ProviderID != nil {
		t.Fatalf("provider not cleared: %v", *n.ProviderID)
	}

	// Absent fields keep values (merge semantics); asn stays after a name-only update.
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(n.ID), map[string]any{"name": "asn-node-2"}); rec.Code != http.StatusOK {
		t.Fatalf("name update: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&n, n.ID).Error; err != nil {
		t.Fatal(err)
	}
	if n.Name != "asn-node-2" || n.ASN != "AS64512" {
		t.Fatalf("merge lost asn: %+v", n)
	}

	// Registration metadata takeover: meta input carries asn too.
	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "asn-node-3", "address": "asn3.example.com", "port": 443,
		"protocol": "vless", "asn": "AS64513",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create 3: %d %s", rec.Code, rec.Body)
	}
	var n3 storage.Node
	if err := db.First(&n3, uint(mustJSON(t, rec)["id"].(float64))).Error; err != nil {
		t.Fatal(err)
	}
	if n3.ASN != "AS64513" || !n3.MetaInit {
		t.Fatalf("asn via meta = %q meta_init=%v", n3.ASN, n3.MetaInit)
	}
}
