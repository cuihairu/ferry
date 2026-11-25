package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/gin-gonic/gin"
)

// echoIP mounts a temporary route on the engine that echoes ClientIP (tests
// only).
func echoIP(t *testing.T, r *gin.Engine) string {
	t.Helper()
	r.GET("/__client_ip", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})
	return "/__client_ip"
}

// getFrom fires one request with the given source address and
// X-Forwarded-For, returning the echoed ClientIP.
func getFrom(t *testing.T, r *gin.Engine, path, remoteAddr, xff string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remoteAddr
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("echo route: %d %s", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// TestClientIPDefaultsToDirectAddr by default no proxy is trusted: X-Forwarded-For
// never participates in the ClientIP decision anywhere (audit fix 2026-11-12, a
// forged header could bypass rate limits / pollute the audit trail).
func TestClientIPDefaultsToDirectAddr(t *testing.T) {
	r, _ := newTestRouterCfg(t, nil) // config.Default() has no TrustedProxies
	path := echoIP(t, r)
	if got := getFrom(t, r, path, "10.1.2.3:5555", "1.2.3.4"); got != "10.1.2.3" {
		t.Fatalf("ClientIP = %q; want direct addr (XFF must be ignored)", got)
	}
}

// TestClientIPHonorsTrustedProxy forwarding headers are only believed from
// reverse-proxy sources inside the whitelist: when 10.1.2.3 is trusted, XFF
// forwarded through it takes effect; a direct forged request (source not in the
// whitelist) does not.
func TestClientIPHonorsTrustedProxy(t *testing.T) {
	r, _ := newTestRouterCfg(t, func(c *config.Config) {
		c.TrustedProxies = []string{"10.1.2.3"}
	})
	path := echoIP(t, r)
	if got := getFrom(t, r, path, "10.1.2.3:5555", "1.2.3.4"); got != "1.2.3.4" {
		t.Fatalf("ClientIP via trusted proxy = %q; want 1.2.3.4", got)
	}
	if got := getFrom(t, r, path, "10.9.9.9:5555", "1.2.3.4"); got != "10.9.9.9" {
		t.Fatalf("ClientIP from untrusted source = %q; want direct addr", got)
	}
}

// TestClientIPInvalidWhitelistFallsClosed a broken whitelist falls back to
// "trust no proxy" (fail closed on the security side): forwarding headers are
// ignored and ClientIP still comes from the direct address.
func TestClientIPInvalidWhitelistFallsClosed(t *testing.T) {
	r, _ := newTestRouterCfg(t, func(c *config.Config) {
		c.TrustedProxies = []string{"not-a-cidr"}
	})
	path := echoIP(t, r)
	if got := getFrom(t, r, path, "10.1.2.3:5555", "1.2.3.4"); got != "10.1.2.3" {
		t.Fatalf("ClientIP with bad whitelist = %q; want direct addr", got)
	}
}
