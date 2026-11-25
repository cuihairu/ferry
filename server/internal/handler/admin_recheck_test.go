package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// seedAdminForJWT inserts an enabled admin row matching adminJWT (user ID 1) --
// the /admin group re-checks the DB (audit fix 2026-11-12), so the token must have
// a real admin behind it, and disabling/demoting/password-changing it invalidates
// old tokens immediately.
func seedAdminForJWT(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&storage.User{
		Username: "root", IsAdmin: true, Enabled: true, AdminRole: RoleOperator,
	}).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
}

// TestAdminAuthRequiresLiveAdminRow covers the three re-check states: no admin row
// behind the token is 401 (previously a valid signature was enough); a row that
// exists and is enabled is 200; once the row is disabled the same token is 401 at
// once -- it no longer survives until natural expiry (audit fix 2026-11-12).
func TestAdminAuthRequiresLiveAdminRow(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// No row: rejected even though the signature is valid.
	if rec := doAdmin(t, r, "GET", "/admin/status", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no row: %d %s; want 401", rec.Code, rec.Body)
	}

	// Row present and enabled: allowed through.
	seedAdminForJWT(t, db)
	if rec := doAdmin(t, r, "GET", "/admin/status", nil); rec.Code != http.StatusOK {
		t.Fatalf("live admin: %d %s; want 200", rec.Code, rec.Body)
	}

	// Disabled: the old token dies instantly.
	if err := db.Model(&storage.User{}).Where("username = ?", "root").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doAdmin(t, r, "GET", "/admin/status", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled admin: %d %s; want 401", rec.Code, rec.Body)
	}

	// Admin flag revoked: also invalid immediately.
	if err := db.Model(&storage.User{}).Where("username = ?", "root").
		Updates(map[string]any{"enabled": true, "is_admin": false}).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doAdmin(t, r, "GET", "/admin/status", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("demoted admin: %d %s; want 401", rec.Code, rec.Body)
	}
}

// TestAdminAuthKilledByPasswordChange verifies invalidation on password change:
// after a real updateAdmin password reset, /admin tokens issued before the reset
// are uniformly 401 (checked with a token fixed up before the reset).
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

	// super resets the password (the /api surface's requireSuper reads the claim role, so doJSON reaches it).
	rec := doJSON(t, r, "PUT", "/api/admins/1", map[string]any{"password": "newpass123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reset password: %d %s", rec.Code, rec.Body)
	}

	// Tokens issued before the reset: invalid immediately.
	if code := adminGet(oldToken); code != http.StatusUnauthorized {
		t.Fatalf("after reset: %d; want 401", code)
	}
}

// TestAdminAuthSurvivesNonSecurityEdits checks that non-security field edits
// (the sign-up quota) do not invalidate the session -- invalidation only hangs off
// disable/demote/password-change, so routine operations are not disrupted.
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
