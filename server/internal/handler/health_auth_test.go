package handler

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// getHealthFrom 以指定来源 IP 与凭据请求 /api/health（IPv6 地址加括号成
// 合法 RemoteAddr，与真实连接格式一致）。
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

// TestHealthAuthGate 覆盖 P2 health 未鉴权修复：环回探活（部署脚本
// start.sh/各 smoke 以 127.0.0.1 在管理员登录前探活，无凭据可用）放行 200；
// 远端调用方无凭据 401、坏凭据 401、有效管理凭据 200——修复前 /api/health
// 裸挂无鉴权，公网可读 agents_online/备份新鲜度/DB 可写等运行态。
func TestHealthAuthGate(t *testing.T) {
	r := newTestRouter(t)

	// 环回 + 无凭据：部署探活场景，必须放行。
	if rec := getHealthFrom(t, r, "127.0.0.1", ""); rec.Code != http.StatusOK {
		t.Fatalf("loopback no-token health: %d %s", rec.Code, rec.Body)
	}
	// IPv6 环回同样放行。
	if rec := getHealthFrom(t, r, "::1", ""); rec.Code != http.StatusOK {
		t.Fatalf("ipv6 loopback no-token health: %d %s", rec.Code, rec.Body)
	}

	// 远端 + 无凭据：必须 401（此前公网可读）。
	if rec := getHealthFrom(t, r, "203.0.113.9", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("remote no-token health: %d %s", rec.Code, rec.Body)
	}
	// 远端 + 坏凭据：必须 401。
	if rec := getHealthFrom(t, r, "203.0.113.9", "wrong-token"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("remote bad-token health: %d %s", rec.Code, rec.Body)
	}
	// 远端 + 有效管理 JWT：放行 200。
	if rec := getHealthFrom(t, r, "203.0.113.9", adminJWT(t)); rec.Code != http.StatusOK {
		t.Fatalf("remote valid-token health: %d %s", rec.Code, rec.Body)
	}
}
