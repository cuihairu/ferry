package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
)

// TestPanelSupportSnapshotExpiry 客服快照带到期日期：开通用户的快照备注
// 含「长期有效」之外的到期日（同步进 servify 客户资料）。
func TestPanelSupportSnapshotExpiry(t *testing.T) {
	var notes atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/guest-sessions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-1","token_type":"bearer","expires_at":1900000000}`))
	})
	mux.HandleFunc("GET /api/customers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	mux.HandleFunc("POST /api/customers", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		notes.Store(body["notes"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	r, _ := newTestRouterCfg(t, func(c *config.Config) {
		c.SupportURL = srv.URL
		c.SupportServiceKey = "sk-test"
	})
	// 到期用户（仍启用）：快照备注应带到期日。
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "exp-user", "quota_bytes": 1 << 30, "expires_at": "2026-12-31T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	token := u["sub_token"].(string)

	rec = doPanel(t, r, token, "GET", "/api/panel/support", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("support: %d %s", rec.Code, rec.Body)
	}
	got, _ := notes.Load().(string)
	if !strings.Contains(got, "2026-12-31") {
		t.Fatalf("notes = %q; want expiry date", got)
	}
}
