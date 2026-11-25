package handler

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// getHealthFrom requests /api/health from a given source IP and with given credentials (an IPv6 address gets wrapped
// in brackets to form a legal RemoteAddr, matching the real-connection format).
func getHealthFrom(t *testing.T, r *gin.Engine, ip, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/health", nil)
	host := ip
	if net.ParseIP(ip).To4() == nil {
		host = "[" + ip + "]"
	}
	req.RemoteAddr = host + ":1234"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestHealthAuthGate covers the P2 fix for /api/health having no auth: loopback liveness probes (the deploy
// script start.sh and the smoke tests probe 127.0.0.1 before the admin login, with no credential available)
// pass with 200; a remote caller without a credential gets 401, with a bad credential 401, and with a valid
// admin credential 200 — before the fix /api/health was mounted bare with no auth, exposing runtime state such as
// agents_online, backup freshness and DB writability to the public internet.
func TestHealthAuthGate(t *testing.T) {
	r := newTestRouter(t)

	// Loopback + no credential: the deploy-liveness scenario, must be allowed.
	if rec := getHealthFrom(t, r, "127.0.0.1", ""); rec.Code != http.StatusOK {
		t.Fatalf("loopback no-token health: %d %s", rec.Code, rec.Body)
	}
	// The IPv6 loopback is allowed too.
	if rec := getHealthFrom(t, r, "::1", ""); rec.Code != http.StatusOK {
		t.Fatalf("ipv6 loopback no-token health: %d %s", rec.Code, rec.Body)
	}

	// Remote + no credential: must be 401 (previously publicly readable).
	if rec := getHealthFrom(t, r, "203.0.113.9", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("remote no-token health: %d %s", rec.Code, rec.Body)
	}
	// Remote + bad credential: must be 401.
	if rec := getHealthFrom(t, r, "203.0.113.9", "wrong-token"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("remote bad-token health: %d %s", rec.Code, rec.Body)
	}
	// Remote + valid admin JWT: allowed with 200.
	if rec := getHealthFrom(t, r, "203.0.113.9", adminJWT(t)); rec.Code != http.StatusOK {
		t.Fatalf("remote valid-token health: %d %s", rec.Code, rec.Body)
	}
}
