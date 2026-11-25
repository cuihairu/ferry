package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
)

// stubServifyOK is a minimal servify stand-in: both the guest/session and customers paths are observable.
func stubServifyOK(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/guest/session", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("X-API-Key") != "sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			SessionID string `json:"session_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-1", "token_type": "bearer", "expires_at": 1900000000,
		})
	})
	mux.HandleFunc("GET /api/customers", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	})
	mux.HandleFunc("POST /api/customers", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["email"] != "u1@users.ferry.local" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPanelSupportDisabledByDefault(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	token := panelToken(t, r, "dora")

	var hits atomic.Int32
	_ = stubServifyOK(t, &hits) // alive but must never be called
	rec := doPanel(t, r, token, "GET", "/api/panel/support", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("support: %d %s", rec.Code, rec.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["enabled"] != false {
		t.Fatalf("enabled = %v, want false", out["enabled"])
	}
	if hits.Load() != 0 {
		t.Fatalf("servify called %d times with support disabled", hits.Load())
	}
}

func TestPanelSupportEnabledPayload(t *testing.T) {
	var hits atomic.Int32
	srv := stubServifyOK(t, &hits)
	r, _ := newTestRouterCfg(t, func(c *config.Config) {
		c.SupportURL = srv.URL
		c.SupportServiceKey = "sk-test"
	})
	token := panelToken(t, r, "eva")

	rec := doPanel(t, r, token, "GET", "/api/panel/support", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("support: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Enabled       bool   `json:"enabled"`
		URL           string `json:"url"`
		SessionID     string `json:"session_id"`
		AccessToken   string `json:"access_token"`
		ExpiresAt     int64  `json:"expires_at"`
		ContextSynced bool   `json:"context_synced"`
		Icon          string `json:"icon"`
		Color         string `json:"color"`
		Theme         string `json:"theme"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Enabled || out.SessionID != "ferry_user_1" || out.AccessToken != "tok-1" {
		t.Fatalf("payload = %+v", out)
	}
	if !out.ContextSynced || out.Icon != "headset" || out.Color != "#6e79d6" || out.Theme != "auto" {
		t.Fatalf("payload = %+v", out)
	}
	if hits.Load() < 3 { // search + create + guest-session
		t.Fatalf("servify calls = %d, want >=3", hits.Load())
	}
}

func TestPanelSupportDegradesWhenServifyDown(t *testing.T) {
	// Point at a closed port: when support is unreachable, degrade to enabled=false
	// without disturbing the panel main flow.
	srv := httptest.NewServer(http.NewServeMux())
	srv.Close()
	r, _ := newTestRouterCfg(t, func(c *config.Config) {
		c.SupportURL = srv.URL
		c.SupportServiceKey = "sk-test"
	})
	token := panelToken(t, r, "frida")

	rec := doPanel(t, r, token, "GET", "/api/panel/support", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("support: %d %s", rec.Code, rec.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["enabled"] != false {
		t.Fatalf("enabled = %v, want false on servify down", out["enabled"])
	}
}

func TestPanelSupportRequiresIdentity(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doPanel(t, r, "", "GET", "/api/panel/support", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("anon support: %d, want 404", rec.Code)
	}
}
