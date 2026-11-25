package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// rlRaw issues a raw-body request for rule lib push (octet-stream, with admin JWT).
func rlRaw(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestRuleLibPushDBErrors covers the rule lib push path: snapshot record
// creation failure 500 (the push chain runs after creation, so an injected
// error breaks it) and version history fetch failure 500.
func TestRuleLibPushDBErrors(t *testing.T) {
	// snapshot record creation failure -> 500.
	r, db := newTestRouterWithDB(t)
	_, _ = createNodeFor(t, r, "rl-db")
	db.Callback().Create().Before("gorm:create").Register("t:rlcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.NodeConfig); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := rlRaw(t, r, "POST", "/api/nodes/1/rulelib?proc=xray&name=geoip.dat", "dat"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}

	// version history fetch failure -> 500.
	r2, db2 := newTestRouterWithDB(t)
	regQueryFail("t:rllist", db2, (*[]storage.NodeConfig)(nil))
	if rec := doJSON(t, r2, "GET", "/api/nodes/1/rulelib", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list fail: %d %s", rec.Code, rec.Body)
	}
}

// TestRuleLibPushTimeout covers agent online but no ack -> 504 (the hub 60s
// wait-timeout branch), snapshot set to failed. The wait duration comes from
// the rulelibAckTimeout constant, so this case inherently takes ~60s.
func TestRuleLibPushTimeout(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "rl-to")
	srv := httptest.NewServer(r)
	defer srv.Close()
	fakeAgent(t, srv.URL, "rl-to", token) // handshake only, never answers

	rec := rlRaw(t, r, "POST", fmt.Sprintf("/api/nodes/%d/rulelib?proc=xray&name=geoip.dat", id), "dat")
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout should 504: %d %s", rec.Code, rec.Body)
	}
	var row storage.NodeConfig
	if err := db.Where("node_id=? AND kind=?", id, "rulelib:geoip.dat").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "failed" {
		t.Fatalf("snapshot status = %s, want failed", row.Status)
	}
}

// TestRoutingAdsSaveError covers blocking toggle save failure 500 (SetSetting
// goes through Save's Update chain).
func TestRoutingAdsSaveError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	db.Callback().Update().Before("gorm:update").Register("t:adssave", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Setting); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, "PUT", "/api/routing/ads", map[string]any{"enabled": true}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("ads save: %d %s", rec.Code, rec.Body)
	}
}
