package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

// TestAdminLoginGuards 覆盖管理员登录错误面：限速锁定 429、坏载荷 400、
// 未知用户与无管理员权限 401。
func TestAdminLoginGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// 坏载荷 → 400。
	if rec := doAdmin(t, r, "POST", "/admin/login", []byte("{bad")); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}

	// 未知用户 → 401（防枚举统一文案）。
	rec := doPanel(t, r, "", "POST", "/admin/login", map[string]any{"username": "ghost", "password": "x"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user: %d %s", rec.Code, rec.Body)
	}

	// 停用/无管理员权限的有效账号 → 401。
	r2, db2 := newTestRouterWithDB(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("pw-plain"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := db2.Create(&storage.User{Username: "plain", SubToken: "plain-sub",
		Password: string(hash), Enabled: true, IsAdmin: false}).Error; err != nil {
		t.Fatal(err)
	}
	rec = doPanel(t, r2, "", "POST", "/admin/login", map[string]any{"username": "plain", "password": "pw-plain"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin: %d %s", rec.Code, rec.Body)
	}

	// 窗口内 5 次失败后锁定 → 429。
	r3, _ := newTestRouterWithDB(t)
	for i := 0; i < 5; i++ {
		rec = doPanel(t, r3, "", "POST", "/admin/login", map[string]any{"username": "ghost", "password": "x"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("fail #%d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	rec = doPanel(t, r3, "", "POST", "/admin/login", map[string]any{"username": "ghost", "password": "x"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("lockout: %d %s", rec.Code, rec.Body)
	}
}

// TestAdminLoginDBErrors 覆盖用户定位取数失败按统一 401 口径（不冒库错）。
func TestAdminLoginDBErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdmin(t, db, "root-err", "pw-root")
	regQueryFail("t:admfind", db, (*storage.User)(nil))
	rec := doPanel(t, r, "", "POST", "/admin/login", map[string]any{"username": "root-err", "password": "pw-root"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("db fail: %d %s", rec.Code, rec.Body)
	}
}
