package handler

import (
	"net/http"
	"testing"
)

// TestApiTokenRateLimit 覆盖签发限流：每 IP 窗口内 100 次放行后第 101 次 429。
// 限流器是包级共享态，先复位并测试后复位，避免受他例顺序影响。
func TestApiTokenRateLimit(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	const ip = "192.0.2.1" // httptest 的 RemoteAddr 网段，与 doJSON 请求同源
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
