package handler

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch 31 coverage fill: error policy for the admin management surface (P2-2) --
// operator quota helper with a missing account and a failed count, list fetch
// failure, creation with bad binding / empty or overlong name / name-check DB
// failure / overlong password rejected by bcrypt / insert failure, update with a
// bad id / not found / locate DB failure / bad binding / short password / overlong
// password / negative quota / empty payload / write-back failure, and delete with a
// bad id / not found / locate DB failure / non-admin row / insert failure.
// Left alone: the crypto/rand error in RandomToken and the isDup unique-constraint
// race (ensureUsernameFree runs first, so normal flow cannot reach it) are not
// forcibly provoked.
// Bug fixed: a missing admin used to surface gorm.ErrRecordNotFound as a 500 via
// replyFind (errNotFound is a different sentinel); it is now explicitly detected as
// a missing gorm record and answered with 404.

// seedAdminRow inserts one sub-admin row directly and returns its ID.
func seedAdminRow(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	row := storage.User{Username: name, SubToken: name + "-sub", Enabled: true,
		IsAdmin: true, AdminRole: RoleOperator, MaxUsers: 2}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed admin %s: %v", name, err)
	}
	return row.ID
}

// TestCheckOperatorQuotaErrors covers the quota helper: missing operator and failed count.
func TestCheckOperatorQuotaErrors(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	if err := h.checkOperatorQuota("ghost"); err == nil ||
		!containsStr(err.Error(), "operator account not found") {
		t.Fatalf("missing operator: %v", err)
	}

	// Count failure: locating the operator (1st query) passes, the quota count (2nd query) is faulted.
	seedAdminRow(t, db, "quota-op")
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:user_query_at", func(tx *gorm.DB) {
		if isUserModel(tx.Statement.Model) || isUserModel(tx.Statement.Dest) {
			seen++
			if seen == 2 {
				tx.AddError(errors.New("db down"))
			}
		}
	})
	if err := h.checkOperatorQuota("quota-op"); err == nil {
		t.Fatal("count error: want err")
	}
}

// TestListAdminsError: list fetch failure is 500.
func TestListAdminsError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminRow(t, db, "list-op")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/admins", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateAdminErrors covers creation: bad binding, empty/overlong name, name-check
// DB failure, an overlong password rejected by bcrypt, and insert failure.
func TestCreateAdminErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/admins", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		name string
		body map[string]any
		msg  string
	}{
		{"empty name", map[string]any{"username": "  ", "password": "password123"}, "username is required"},
		{"long name", map[string]any{"username": strings.Repeat("a", 65), "password": "password123"}, "1-64 chars"},
	} {
		if rec := doJSON(t, r, http.MethodPost, "/api/admins", tc.body); rec.Code != http.StatusBadRequest ||
			!containsStr(rec.Body.String(), tc.msg) {
			t.Fatalf("%s: %d %s", tc.name, rec.Code, rec.Body)
		}
	}

	// Name-check DB failure (ensureUsernameFree's Count lands on the users table).
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:user_count_q", isUserModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/admins",
		map[string]any{"username": "q-fail", "password": "password123"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("name check: %d %s", rec.Code, rec.Body)
	}

	// Overlong password rejected by bcrypt (bcrypt errors above 72 bytes).
	r, db = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/admins",
		map[string]any{"username": "long-pw", "password": strings.Repeat("p", 80)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("long password: %d %s", rec.Code, rec.Body)
	}

	// Insert failure.
	r, db = newTestRouterWithDB(t)
	failCreateOn(t, db, "test:user_create", isUserModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/admins",
		map[string]any{"username": "c-fail", "password": "password123"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateAdminErrors covers update: bad id, not found 404, locate DB failure 500,
// bad binding, short/overlong password, negative quota, empty payload, and
// write-back failure.
func TestUpdateAdminErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/abc",
		map[string]any{"enabled": false}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// Not found: DB failures other than not-found also follow the 500 policy (closeSQL below).
	r, _ = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/9999",
		map[string]any{"enabled": false}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedAdminRow(t, db, "upd-op")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1",
		map[string]any{"enabled": false}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("locate: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedAdminRow(t, db, "bind-op")
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1",
		map[string]any{"password": "short"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("short pw: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1",
		map[string]any{"password": strings.Repeat("p", 80)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("long pw: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1",
		map[string]any{"max_users": -1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("neg quota: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: %d %s", rec.Code, rec.Body)
	}

	// Write-back failure.
	r, db = newTestRouterWithDB(t)
	seedAdminRow(t, db, "wb-op")
	failUpdateOn(t, db, "test:user_update", isUserModel)
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1",
		map[string]any{"enabled": false}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("update fail: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteAdminErrors covers delete (demotion): bad id, not found 404, locate DB
// failure 500, non-admin row 404, and demotion insert failure.
func TestDeleteAdminErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/admins/abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, _ = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/admins/9999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedAdminRow(t, db, "del-op")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodDelete, "/api/admins/1", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("locate: %d %s", rec.Code, rec.Body)
	}

	// Non-admin row: 404 (does not reveal whether that id exists).
	r, db = newTestRouterWithDB(t)
	plain := storage.User{Username: "plain-user", SubToken: "plain-sub", Enabled: true}
	if err := db.Create(&plain).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, http.MethodDelete, "/api/admins/1", nil); rec.Code != http.StatusNotFound ||
		!containsStr(rec.Body.String(), "not an admin") {
		t.Fatalf("not admin: %d %s", rec.Code, rec.Body)
	}

	// Demotion insert failure.
	r, db = newTestRouterWithDB(t)
	seedAdminRow(t, db, "demote-op")
	failUpdateOn(t, db, "test:user_demote", isUserModel)
	if rec := doJSON(t, r, http.MethodDelete, "/api/admins/1", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("demote fail: %d %s", rec.Code, rec.Body)
	}
}
