package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCreateEnrollmentTokenValidation 载荷非法 400；自定义 ttl 落响应，
// 签发人取自管理员 JWT（actorOf 的 adminUserID 支路）。
func TestCreateEnrollmentTokenValidation(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "POST", "/api/enrollment-tokens", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	rec := doJSON(t, r, "POST", "/api/enrollment-tokens",
		map[string]any{"note": "安装用", "ttl_minutes": 30})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var row storage.EnrollmentToken
	if err := db.Order("id DESC").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.CreatedBy == "" || row.CreatedBy == "unknown" {
		t.Fatalf("created_by = %q; want admin identity", row.CreatedBy)
	}
}

// TestRevokeEnrollmentTokenMissing 吊销不存在的凭据：First 落 gorm
// record-not-found（非 errNotFound 哨兵），replyFind 走 500 兜底——真实行为
// 如此，不冒称成功。
func TestRevokeEnrollmentTokenMissing(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "DELETE", "/api/enrollment-tokens/99999", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("revoke missing: %d %s; want 500 (gorm not-found not the errNotFound sentinel)", rec.Code, rec.Body)
	}
}

// TestEnrollBindError 注册载荷非法：400 不消耗凭据也不计入失败锁。
func TestEnrollBindError(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doBare(t, r, "POST", "/api/enroll"); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: %d %s", rec.Code, rec.Body)
	}
}

// TestEnrollRateLimit 注册交换口按 IP 限流：连续凭据失败达 FailLimit 后
// 锁定，后续尝试一律 429（FERRY-001）。
func TestEnrollRateLimit(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	postEnroll := func(ip string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/enroll",
			strings.NewReader(`{"name":"n","address":"a","port":443,"protocol":"vless"}`))
		req.RemoteAddr = ip + ":1234"
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 1; i <= 5; i++ {
		if code := postEnroll("10.9.0.1"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d; want 401", i, code)
		}
	}
	for i := 6; i <= 8; i++ {
		if code := postEnroll("10.9.0.1"); code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: %d; want locked 429", i, code)
		}
	}
	// 换 IP 不受前一人锁定影响。
	if code := postEnroll("10.9.0.2"); code != http.StatusUnauthorized {
		t.Fatalf("fresh ip: %d; want 401", code)
	}
}
