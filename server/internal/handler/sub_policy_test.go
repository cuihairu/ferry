package handler

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSubscriptionPolicyFilter covers the delivery-side enforcement of
// F-0288/F-0291 (region + health constraints) and F-0286 (compact output):
// a user-scope policy trims the entry list the subscription serves.
func TestSubscriptionPolicyFilter(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "polsub", "quota_bytes": 1000000, "expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	user := mustJSON(t, rec)
	uid := uint(user["id"].(float64))
	token := user["sub_token"].(string)

	// Two entry nodes: jp-online (allowed) and hk-offline (filtered by both
	// the region whitelist and the health gate).
	mkNode := func(name, region, status string) {
		rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
			"config": `{"uuid":"u-1","tls":true,"sni":"s.com","net":"ws","path":"/wss","host":"cdn.example.com"}`,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create node %s: %d %s", name, rec.Code, rec.Body)
		}
		if err := db.Model(&storage.Node{}).Where("name = ?", name).
			Updates(map[string]any{
				"role": "entry", "meta_init": true, "region": region, "status": status,
			}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mkNode("jp-online", "JP", "online")
	mkNode("hk-offline", "HK", "offline")

	// User policy: regions=JP, min_health=healthy.
	rec = doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope": "user", "subject_id": uid,
		"constraints": map[string]any{"regions": []string{"JP"}, "min_health": "healthy"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create policy: %d %s", rec.Code, rec.Body)
	}

	decoded := func(rec *httptest.ResponseRecorder) string {
		t.Helper()
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rec.Body.String()))
		if err != nil {
			t.Fatalf("body not base64: %v (%s)", err, rec.Body)
		}
		return string(raw)
	}

	body := getSub(t, r, "/sub/"+token, "v2rayNG/1.8")
	if body.Code != http.StatusOK {
		t.Fatalf("sub fetch: %d %s", body.Code, body.Body)
	}
	raw := decoded(body)
	if strings.Contains(raw, "hk-offline.example.com") {
		t.Fatalf("policy-violating node leaked into subscription: %s", raw)
	}
	if !strings.Contains(raw, "jp-online.example.com") {
		t.Fatalf("allowed node missing from subscription: %s", raw)
	}

	// No policy on the chain (delete it): both nodes come back.
	var pol storage.Policy
	if err := db.Where("scope = ?", "user").First(&pol).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "DELETE", "/api/policies/"+utoa(pol.ID), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete policy: %d %s", rec.Code, rec.Body)
	}
	body = getSub(t, r, "/sub/"+token, "v2rayNG/1.8")
	if !strings.Contains(decoded(body), "hk-offline.example.com") {
		t.Fatalf("unconstrained subscription missing hk node: %s", body.Body)
	}
}
