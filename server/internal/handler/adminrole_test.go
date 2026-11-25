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

// Multi-tier admin / sub-admin quota (P2-2) tests: super runs sub-admin CRUD
// (demotion does not delete the row, super itself cannot be edited/demoted), an
// operator's tiered JWT takes effect after login (super surface 403), and the
// max_users sign-up quota is charged by created_by (0 = unlimited).

// opJWT mints an operator-tier token directly (cases through the real login path are tested elsewhere).
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

// TestAdminRoleCRUD covers the admin management surface: an operator overstepping
// gets 403, the full CRUD chain, super self-lockout protection (cannot be edited or
// demoted), a disabled account is refused at login, and demotion keeps the user row.
func TestAdminRoleCRUD(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// super: create a sub-admin (quota 2).
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
	// Duplicate username 409, short password 400, negative quota 400.
	if rec = doJSON(t, r, "POST", "/api/admins", map[string]any{"username": "subop", "password": "password123"}); rec.Code != http.StatusConflict {
		t.Fatalf("dup: %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/admins", map[string]any{"username": "x", "password": "short"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("short pw: %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/admins", map[string]any{"username": "x", "password": "password123", "max_users": -1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("neg quota: %d", rec.Code)
	}

	// Directly minted operator token: the whole /api/admins surface is 403 (tier gate).
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

	// Real login path: operator login yields a tiered token (no 2FA, so it passes straight through).
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
	// Login refused after disabling (the users.enabled re-check is in the login chain).
	if rec = doJSON(t, r, "PUT", "/api/admins/"+idStr, map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	if rec = doPanel(t, r, "", "POST", "/admin/login", map[string]any{"username": "subop", "password": "password123"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled login: %d", rec.Code)
	}
	// Re-enable + password change + quota reset in one PUT.
	if rec = doJSON(t, r, "PUT", "/api/admins/"+idStr, map[string]any{"enabled": true, "max_users": 0, "password": "newpassword9"}); rec.Code != http.StatusOK {
		t.Fatalf("re-enable: %d %s", rec.Code, rec.Body)
	}
	// The new password can log in.
	if rec = doPanel(t, r, "", "POST", "/admin/login", map[string]any{"username": "subop", "password": "newpassword9"}); rec.Code != http.StatusOK {
		t.Fatalf("new password login: %d %s", rec.Code, rec.Body)
	}

	// super itself is protected against self-lockout: cannot be edited or demoted.
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
	// A non-admin user is not in the admin roster (handled as not found).
	plain := storage.User{Username: "plain1", SubToken: "st-p1", Enabled: true}
	if err := db.Create(&plain).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "PUT", "/api/admins/"+strconv.Itoa(int(plain.ID)), map[string]any{"enabled": false}); rec.Code != http.StatusNotFound {
		t.Fatalf("edit non-admin: %d", rec.Code)
	}

	// Demotion: the identity is stripped without deleting the row, user data preserved.
	if rec = doJSON(t, r, "DELETE", "/api/admins/"+idStr, nil); rec.Code != http.StatusOK {
		t.Fatalf("demote: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	db.Where("username = ?", "subop").First(&u)
	if u.IsAdmin || u.AdminRole != "" || u.ID == 0 {
		t.Fatalf("demoted row = %+v（应保留为普通用户）", u)
	}
}

// TestOperatorUserQuota covers the sub-admin sign-up quota: with max_users=2, once
// filled further creation is 403, created_by is recorded, raising the quota allows
// more, max_users=0 is unlimited, and super has no quota concept at all.
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
	// Quota exhausted.
	if got := mk(op, "q3"); got != http.StatusForbidden {
		t.Fatalf("q3 over quota: %d want 403", got)
	}
	// created_by attribution.
	var q1 storage.User
	db.Where("username = ?", "q1").First(&q1)
	if q1.CreatedBy != "op2" {
		t.Fatalf("created_by = %q", q1.CreatedBy)
	}
	// Can continue after raising the quota.
	var opRow storage.User
	db.Where("username = ? AND is_admin = ?", "op2", true).First(&opRow)
	if rec := doJSON(t, r, "PUT", "/api/admins/"+strconv.Itoa(int(opRow.ID)), map[string]any{"max_users": 0}); rec.Code != http.StatusOK {
		t.Fatalf("raise quota: %d %s", rec.Code, rec.Body)
	}
	if got := mk(op, "q3"); got != http.StatusCreated {
		t.Fatalf("q3 after raise: %d", got)
	}
	// super has no quota concept when creating users.
	if got := mk(super, "s1"); got != http.StatusCreated {
		t.Fatalf("super mk: %d", got)
	}
	// List surface: readable by super.
	if rec := doJSON(t, r, "GET", "/api/admins", nil); rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}
