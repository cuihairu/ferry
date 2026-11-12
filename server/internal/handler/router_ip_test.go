package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/gin-gonic/gin"
)

// echoIP 在引擎上挂一条回显 ClientIP 的临时路由（仅测试用）。
func echoIP(t *testing.T, r *gin.Engine) string {
	t.Helper()
	r.GET("/__client_ip", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})
	return "/__client_ip"
}

// getFrom 以指定来源地址与 X-Forwarded-For 打一次请求，返回回显的 ClientIP。
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

// TestClientIPDefaultsToDirectAddr 默认不信任任何代理：X-Forwarded-For
// 全线不参与 ClientIP 判定（审核修复 2026-11-12，伪造头绕过限流/污染审计）。
func TestClientIPDefaultsToDirectAddr(t *testing.T) {
	r, _ := newTestRouterCfg(t, nil) // config.Default() 无 TrustedProxies
	path := echoIP(t, r)
	if got := getFrom(t, r, path, "10.1.2.3:5555", "1.2.3.4"); got != "10.1.2.3" {
		t.Fatalf("ClientIP = %q; want direct addr (XFF must be ignored)", got)
	}
}

// TestClientIPHonorsTrustedProxy 白名单内的反代来源才采信转发头：
// 信任 10.1.2.3 时经它转发的 XFF 生效；直连伪造（来源不在白名单）不生效。
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

// TestClientIPInvalidWhitelistFallsClosed 白名单写坏回落「不信任任何代理」
// （安全侧）：转发头失效，ClientIP 仍取直连地址。
func TestClientIPInvalidWhitelistFallsClosed(t *testing.T) {
	r, _ := newTestRouterCfg(t, func(c *config.Config) {
		c.TrustedProxies = []string{"not-a-cidr"}
	})
	path := echoIP(t, r)
	if got := getFrom(t, r, path, "10.1.2.3:5555", "1.2.3.4"); got != "10.1.2.3" {
		t.Fatalf("ClientIP with bad whitelist = %q; want direct addr", got)
	}
}
