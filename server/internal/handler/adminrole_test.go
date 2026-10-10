package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/golang-jwt/jwt/v5"
)

// 多级管理员/子管理员配额（P2-2）测试：super 管理子管理员 CRUD（降级不删
// 行、super 本体不可改/降）、operator 登录后层级 JWT 生效（super 面 403）、
// max_users 建用户配额按 created_by 归账（0=不限）。

// opJWT 直铸 operator 层级令牌（真实登录路径的用例另测）。
func opJWT(t *testing.T, subject string) string {
	t.Helper()
	claims := adminClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			ID:        "999",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		IsAdmin: true,
		Role:    "operator",
	}
	ss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("ferry-admin-secret"))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return ss
}

// TestAdminRoleCRUD 覆盖管理员管理面：operator 越权 403、CRUD 全链、
// super 防自锁（不可改/不可降）、停用登录被拒、降级保留用户行。
func TestAdminRoleCRUD(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// super：建子管理员（配额 2）。
	rec := doJSON(t, r, "POST", "/api/admins", map[string]any{
		"username": "subop", "password": "password123", "max_users": 2,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create admin: %d %s", rec.Code, rec.Body)
	}
	var row struct {
		ID       uint   `json:"id"`
		Role     string `json:"role"`
		MaxUsers int    `json:"max_users"`
	}
	json.Unmarshal(rec.Body.Bytes(), &row)
	if row.Role != "operator" || row.MaxUsers != 2 {
		t.Fatalf("row = %+v", row)
	}
	// 重复用户名 409、短密码 400、负配额 400。
	if rec = doJSON(t, r, "POST", "/api/admins", map[string]any{"username": "subop", "password": "password123"}); rec.Code != http.StatusConflict {
		t.Fatalf("dup: %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/admins", map[string]any{"username": "x", "password": "short"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("short pw: %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/admins", map[string]any{"username": "x", "password": "password123", "max_users": -1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("neg quota: %d", rec.Code)
	}

	// operator 直铸令牌：/api/admins 面全 403（层级门槛）。
	op := opJWT(t, "subop")
	idStr := strconv.Itoa(int(row.ID))
	for _, tc := range []struct{ m, p string }{
		{"GET", "/api/admins"}, {"POST", "/api/admins"},
		{"PUT", "/api/admins/" + idStr}, {"DELETE", "/api/admins/" + idStr},
	} {
		if rec = doPanel(t, r, op, tc.m, tc.p, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("operator %s %s: %d want 403", tc.m, tc.p, rec.Code)
		}
	}

	// 真实登录路径：operator 登录拿到层级令牌（无 2FA 直过）。
	rec = doPanel(t, r, "", "POST", "/admin/login", map[string]any{"username": "subop", "password": "password123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("operator login: %d %s", rec.Code, rec.Body)
	}
	var lg struct {
		Token string `json:"token"`
	}
	json.Unmarshal(rec.Body.Bytes(), &lg)
	if rec = doPanel(t, r, lg.Token, "GET", "/api/admins", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("logged-in operator /admins: %d", rec.Code)
	}
	// 停用后登录被拒（users.enabled 复查在登录链）。
	if rec = doJSON(t, r, "PUT", "/api/admins/"+idStr, map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	if rec = doPanel(t, r, "", "POST", "/admin/login", map[string]any{"username": "subop", "password": "password123"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled login: %d", rec.Code)
	}
	// 恢复+改密+清配额一次 PUT。
	if rec = doJSON(t, r, "PUT", "/api/admins/"+idStr, map[string]any{"enabled": true, "max_users": 0, "password": "newpassword9"}); rec.Code != http.StatusOK {
		t.Fatalf("re-enable: %d %s", rec.Code, rec.Body)
	}
	// 新密码可登录。
	if rec = doPanel(t, r, "", "POST", "/admin/login", map[string]any{"username": "subop", "password": "newpassword9"}); rec.Code != http.StatusOK {
		t.Fatalf("new password login: %d %s", rec.Code, rec.Body)
	}

	// super 本体防自锁：不可改、不可降。
	sup := storage.User{Username: "boss", SubToken: "st-boss", Password: "x", Enabled: true, IsAdmin: true, AdminRole: "super"}
	if err := db.Create(&sup).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "PUT", "/api/admins/"+strconv.Itoa(int(sup.ID)), map[string]any{"enabled": false}); rec.Code != http.StatusForbidden {
		t.Fatalf("edit super: %d", rec.Code)
	}
	if rec = doJSON(t, r, "DELETE", "/api/admins/"+strconv.Itoa(int(sup.ID)), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("demote super: %d", rec.Code)
	}
	// 非 admin 用户不在管理员名册（按 not found 处理）。
	plain := storage.User{Username: "plain1", SubToken: "st-p1", Enabled: true}
	if err := db.Create(&plain).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "PUT", "/api/admins/"+strconv.Itoa(int(plain.ID)), map[string]any{"enabled": false}); rec.Code != http.StatusNotFound {
		t.Fatalf("edit non-admin: %d", rec.Code)
	}

	// 降级：摘身份不删行，用户数据保留。
	if rec = doJSON(t, r, "DELETE", "/api/admins/"+idStr, nil); rec.Code != http.StatusOK {
		t.Fatalf("demote: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	db.Where("username = ?", "subop").First(&u)
	if u.IsAdmin || u.AdminRole != "" || u.ID == 0 {
		t.Fatalf("demoted row = %+v（应保留为普通用户）", u)
	}
}

// TestOperatorUserQuota 覆盖子管理员建用户配额：max_users=2 建满后 403、
// 落 created_by、提额后可继续、max_users=0 不限、super 无配额概念。
func TestOperatorUserQuota(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	super := adminJWT(t)

	if rec := doJSON(t, r, "POST", "/api/admins", map[string]any{"username": "op2", "password": "password123", "max_users": 2}); rec.Code != http.StatusCreated {
		t.Fatalf("mk operator: %d %s", rec.Code, rec.Body)
	}
	op := opJWT(t, "op2")

	mk := func(tok, name string) int {
		return doPanel(t, r, tok, "POST", "/api/users", map[string]any{"username": name}).Code
	}
	if got := mk(op, "q1"); got != http.StatusCreated {
		t.Fatalf("q1: %d", got)
	}
	if got := mk(op, "q2"); got != http.StatusCreated {
		t.Fatalf("q2: %d", got)
	}
	// 配额用满。
	if got := mk(op, "q3"); got != http.StatusForbidden {
		t.Fatalf("q3 over quota: %d want 403", got)
	}
	// created_by 归账。
	var q1 storage.User
	db.Where("username = ?", "q1").First(&q1)
	if q1.CreatedBy != "op2" {
		t.Fatalf("created_by = %q", q1.CreatedBy)
	}
	// 提额后可继续。
	var opRow storage.User
	db.Where("username = ? AND is_admin = ?", "op2", true).First(&opRow)
	if rec := doJSON(t, r, "PUT", "/api/admins/"+strconv.Itoa(int(opRow.ID)), map[string]any{"max_users": 0}); rec.Code != http.StatusOK {
		t.Fatalf("raise quota: %d %s", rec.Code, rec.Body)
	}
	if got := mk(op, "q3"); got != http.StatusCreated {
		t.Fatalf("q3 after raise: %d", got)
	}
	// super 建用户无配额概念。
	if got := mk(super, "s1"); got != http.StatusCreated {
		t.Fatalf("super mk: %d", got)
	}
	// 列表面：super 可读。
	if rec := doJSON(t, r, "GET", "/api/admins", nil); rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}
