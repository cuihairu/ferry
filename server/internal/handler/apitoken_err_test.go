package handler

import (
	"net/http"
	"testing"
)

// TestApiTokenRateLimit covers issue rate limiting: 100 attempts per IP within
// the window pass, and the 101st is 429. The limiter is package-level shared
// state, so it is reset beforehand and again in a cleanup, to stay immune to
// other tests' ordering.
func TestApiTokenRateLimit(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	const ip = "192.0.2.1" // httptest's RemoteAddr network, same source as the doJSON requests
	ApiTokenLimiter.Reset(ip)
	t.Cleanup(func() { ApiTokenLimiter.Reset(ip) })

	for i := 1; i <= 100; i++ {
		if rec := doJSON(t, r, http.MethodPost, "/api/token", nil); rec.Code != http.StatusOK {
			t.Fatalf("attempt %d = %d %s, want 200", i, rec.Code, rec.Body)
		}
	}
	if rec := doJSON(t, r, http.MethodPost, "/api/token", nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt 101 = %d %s, want 429", rec.Code, rec.Body)
	}
}
