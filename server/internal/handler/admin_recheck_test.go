package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// seedAdminForJWT 落一条与 adminJWT（用户 ID 1）对齐的启用管理员行——
// /admin 组回库复核（审核修复 2026-11-12）要求令牌背后有真实管理员，
// 停用/降级/改密后旧令牌即刻失效。
func seedAdminForJWT(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&storage.User{
		Username: "root", IsAdmin: true, Enabled: true, AdminRole: RoleOperator,
	}).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
}

// TestAdminAuthRequiresLiveAdminRow 回库复核三态：令牌背后无管理员行 401
// （此前签名有效即放行）；行在且启用 200；行被停用后同一令牌即刻 401
// ——不再存活到自然过期（审核修复 2026-11-12）。
func TestAdminAuthRequiresLiveAdminRow(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 无行：签名合法也拒。
	if rec := doAdmin(t, r, "GET", "/admin/status", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no row: %d %s; want 401", rec.Code, rec.Body)
	}

	// 行在且启用：放行。
	seedAdminForJWT(t, db)
	if rec := doAdmin(t, r, "GET", "/admin/status", nil); rec.Code != http.StatusOK {
		t.Fatalf("live admin: %d %s; want 200", rec.Code, rec.Body)
	}

	// 停用：旧令牌立即失效。
	if err := db.Model(&storage.User{}).Where("username = ?", "root").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doAdmin(t, r, "GET", "/admin/status", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled admin: %d %s; want 401", rec.Code, rec.Body)
	}

	// 摘管理员身份：同样立即失效。
	if err := db.Model(&storage.User{}).Where("username = ?", "root").
		Updates(map[string]any{"enabled": true, "is_admin": false}).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doAdmin(t, r, "GET", "/admin/status", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("demoted admin: %d %s; want 401", rec.Code, rec.Body)
	}
}

// TestAdminAuthKilledByPasswordChange 改密作废：走 updateAdmin 真实改密
// 路径后，改密前签发的 /admin 令牌一律 401（用改密前固定签发的令牌验证）。
func TestAdminAuthKilledByPasswordChange(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	oldToken := adminJWT(t)
	adminGet := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/admin/status", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := adminGet(oldToken); code != http.StatusOK {
		t.Fatalf("before reset: %d; want 200", code)
	}

	// super 改密（/api 面 requireSuper 认 claim 角色，doJSON 即可触达）。
	rec := doJSON(t, r, "PUT", "/api/admins/1", map[string]any{"password": "newpass123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reset password: %d %s", rec.Code, rec.Body)
	}

	// 改密前签发的令牌：立即失效。
	if code := adminGet(oldToken); code != http.StatusUnauthorized {
		t.Fatalf("after reset: %d; want 401", code)
	}
}

// TestAdminAuthSurvivesNonSecurityEdits 非安全字段改动（建号配额）不作废
// 会话——作废只挂在停用/降级/改密三处，避免误伤正常运维。
func TestAdminAuthSurvivesNonSecurityEdits(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	if rec := doJSON(t, r, "PUT", "/api/admins/1", map[string]any{"max_users": 5}); rec.Code != http.StatusOK {
		t.Fatalf("edit max_users: %d %s", rec.Code, rec.Body)
	}
	if rec := doAdmin(t, r, "GET", "/admin/status", nil); rec.Code != http.StatusOK {
		t.Fatalf("after non-security edit: %d %s; want 200", rec.Code, rec.Body)
	}
}
