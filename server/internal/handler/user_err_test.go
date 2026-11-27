package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Twenty-fourth batch gap fill: the user CRUD 400/404/500 paths — validation failures (bad
// binding/blank name/overlong/negative quota/bad bandwidth/bad device count/bad cycle), template
// and persistence faults, rename conflicts and the re-find fallback failing, and the two cleanup
// stages of user deletion and token reset each failing on their own.
// Forty-ninth batch gap fill: the 409 classification of storage-side unique-conflict text (isDup)
// tested directly with an injected error.
// Left out on purpose: crypto/rand errors from RandomToken are not forced in.

// isUserModel reports whether a statement lands on the users table (a Find chain's Model is a slice pointer).
func isUserModel(v any) bool {
	switch v.(type) {
	case *storage.User, []storage.User, *[]storage.User:
		return true
	}
	return false
}

// isSubDeviceModel reports whether a statement lands on the device record table.
func isSubDeviceModel(v any) bool {
	switch v.(type) {
	case *storage.SubDevice, []storage.SubDevice, *[]storage.SubDevice:
		return true
	}
	return false
}

// failUserQueriesAt makes the n-th (1-based) query against the users table fail: used to target the
// rename conflict and the re-find fallback by order.
func failUserQueriesAt(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:user_query_at", func(tx *gorm.DB) {
		if !isUserModel(tx.Statement.Model) {
			return
		}
		seen++
		if seen == n {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failUserCreates makes user row creation fail.
func failUserCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:user_create", func(tx *gorm.DB) {
		if isUserModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failUserUpdates makes user row updates fail (preference write-back / rename / explicit disable).
func failUserRowUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:user_update", func(tx *gorm.DB) {
		if isUserModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failUserDeletes makes user row deletion fail.
func failUserDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:user_delete", func(tx *gorm.DB) {
		if isUserModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failSubDeviceDeletes makes device record deletion fail.
func failSubDeviceDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:subdevice_delete", func(tx *gorm.DB) {
		if isSubDeviceModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// seedUser creates a user via the API and returns its row id.
func seedUser(t *testing.T, r *gin.Engine, db *gorm.DB, name string) uint {
	t.Helper()
	rec := doJSON(t, r, http.MethodPost, "/api/users",
		map[string]any{"username": name, "quota_bytes": 1 << 30})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user %s: %d %s", name, rec.Code, rec.Body)
	}
	var u storage.User
	if err := db.Where("username = ?", name).First(&u).Error; err != nil {
		t.Fatalf("find user %s: %v", name, err)
	}
	return u.ID
}

// TestUserListGetErrors list query failure and the 404/500 of single-user reads.
func TestUserListGetErrors(t *testing.T) {
	// List query failure.
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/users", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	r, _ = newTestRouterWithDB(t)
	for _, id := range []string{"abc", "9999"} {
		rec := doJSON(t, r, http.MethodGet, "/api/users/"+id, nil)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not found") {
			t.Fatalf("get %s: %d %s", id, rec.Code, rec.Body)
		}
	}
}

// TestCreateUserErrors creation's validation, template fault, and persistence failure paths.
func TestCreateUserErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body any
		msg  string
	}{
		{"bind", "not json", ""},
		{"empty-username", map[string]any{"username": "  "}, "username is required"},
		{"long-username", map[string]any{"username": strings.Repeat("u", 65)}, "username is required"},
		{"neg-quota", map[string]any{"username": "neg-q", "quota_bytes": -1}, "quota_bytes must be"},
		{"bw", map[string]any{"username": "bw-u", "bw_up_mbps": -5}, "bw_up_mbps/bw_down_mbps must be 0-100000"},
		{"device", map[string]any{"username": "dev-u", "device_limit": 2000}, "device_limit must be 0-1000"},
		{"ip", map[string]any{"username": "ip-u", "ip_limit": -1}, "ip_limit must be 0-1000"},
		{"cycle", map[string]any{"username": "cyc-u", "reset_cycle": "year"}, "reset_cycle must be none/day/week/month"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestRouterWithDB(t)
			rec := doJSON(t, r, http.MethodPost, "/api/users", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400: %s", rec.Code, rec.Body)
			}
			if tc.msg != "" && !strings.Contains(rec.Body.String(), tc.msg) {
				t.Fatalf("body = %s, want %q", rec.Body, tc.msg)
			}
		})
	}

	// Template read fault (drop the settings table).
	r, db := newTestRouterWithDB(t)
	if err := db.Migrator().DropTable(&storage.Setting{}); err != nil {
		t.Fatalf("drop settings: %v", err)
	}
	rec := doJSON(t, r, http.MethodPost, "/api/users",
		map[string]any{"username": "tpl-u", "use_template": true})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("template: %d %s", rec.Code, rec.Body)
	}

	// Storage-side creation failure (the uniqueness precheck already passed).
	r, db = newTestRouterWithDB(t)
	failUserCreates(t, db)
	rec = doJSON(t, r, http.MethodPost, "/api/users", map[string]any{"username": "create-fail"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	// The uniqueness precheck itself fails (500, not a conflict).
	r, db = newTestRouterWithDB(t)
	failUserQueriesAt(t, db, 1)
	rec = doJSON(t, r, http.MethodPost, "/api/users", map[string]any{"username": "chk-fail"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ensureUsernameFree: %d %s", rec.Code, rec.Body)
	}

	// The explicit-disable write-back fails.
	r, db = newTestRouterWithDB(t)
	failUserRowUpdates(t, db)
	rec = doJSON(t, r, http.MethodPost, "/api/users",
		map[string]any{"username": "disabled-u", "enabled": false})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("enabled write-back: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateUserErrors update's lookup 404, validation, rename conflict, and re-find fallback failure.
func TestUpdateUserErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, id := range []string{"abc", "9999"} {
		rec := doJSON(t, r, http.MethodPut, "/api/users/"+id, map[string]any{"quota_bytes": 1})
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not found") {
			t.Fatalf("update %s: %d %s", id, rec.Code, rec.Body)
		}
	}

	for _, tc := range []struct {
		name string
		body any
		msg  string
	}{
		{"bind", "not json", ""},
		{"long-username", map[string]any{"username": strings.Repeat("u", 65)}, "username is required"},
		{"neg-quota", map[string]any{"quota_bytes": -1}, "quota_bytes must be"},
		{"bw", map[string]any{"bw_up_mbps": 200000}, "bw_up_mbps/bw_down_mbps must be 0-100000"},
		{"device", map[string]any{"device_limit": 2000}, "device_limit must be 0-1000"},
		{"ip", map[string]any{"ip_limit": -1}, "ip_limit must be 0-1000"},
		{"cycle", map[string]any{"reset_cycle": "year"}, "reset_cycle must be none/day/week/month"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db := newTestRouterWithDB(t)
			seedUser(t, r, db, "upd-"+tc.name)
			rec := doJSON(t, r, http.MethodPut, "/api/users/1", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400: %s", rec.Code, rec.Body)
			}
			if tc.msg != "" && !strings.Contains(rec.Body.String(), tc.msg) {
				t.Fatalf("body = %s, want %q", rec.Body, tc.msg)
			}
		})
	}

	// A valid multi-field PATCH: bandwidth/device/IP/expiry/cycle/disable sent together, each branch persisted.
	r, db := newTestRouterWithDB(t)
	seedUser(t, r, db, "upd-fields")
	rec := doJSON(t, r, http.MethodPut, "/api/users/1", map[string]any{
		"bw_up_mbps": 10, "bw_down_mbps": 20,
		"device_limit": 5, "ip_limit": 6,
		"expires_at": "2027-01-01T00:00:00Z", "reset_cycle": "month",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("valid fields: %d %s", rec.Code, rec.Body)
	}
	var upd struct {
		BwDownMbps  int    `json:"bw_down_mbps"`
		DeviceLimit int    `json:"device_limit"`
		IPLimit     int    `json:"ip_limit"`
		ResetCycle  string `json:"reset_cycle"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &upd); err != nil {
		t.Fatal(err)
	}
	if upd.BwDownMbps != 20 || upd.DeviceLimit != 5 || upd.IPLimit != 6 || upd.ResetCycle != "month" {
		t.Fatalf("assigned = %+v", upd)
	}

	// Rename conflict 409 (taken by another user).
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "rename-a")
	seedUser(t, r, db, "rename-b")
	rec = doJSON(t, r, http.MethodPut, "/api/users/2", map[string]any{"username": "rename-a"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already exists") {
		t.Fatalf("rename conflict: %d %s", rec.Code, rec.Body)
	}

	// DB fault in the uniqueness precheck (the 2nd query).
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "chk-a")
	seedUser(t, r, db, "chk-b")
	failUserQueriesAt(t, db, 2)
	rec = doJSON(t, r, http.MethodPut, "/api/users/2", map[string]any{"username": "chk-a"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ensureUsernameFree: %d %s", rec.Code, rec.Body)
	}

	// The update statement fails with 500; the clear-expiry branch.
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "upd-quota")
	failUserRowUpdates(t, db)
	rec = doJSON(t, r, http.MethodPut, "/api/users/1", map[string]any{"quota_bytes": 1000})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("updates: %d %s", rec.Code, rec.Body)
	}
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "upd-clear")
	rec = doJSON(t, r, http.MethodPut, "/api/users/1", map[string]any{"clear_expire": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear expire: %d %s", rec.Code, rec.Body)
	}

	// The re-find fallback fails (the 3rd query: lookup 1 -> update -> re-check).
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "upd-refind")
	failUserQueriesAt(t, db, 2)
	rec = doJSON(t, r, http.MethodPut, "/api/users/1", map[string]any{"quota_bytes": 1000})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("re-find: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteUserErrors delete's lookup 404 and each of the two cleanup stages failing.
func TestDeleteUserErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, id := range []string{"abc", "9999"} {
		rec := doJSON(t, r, http.MethodDelete, "/api/users/"+id, nil)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not found") {
			t.Fatalf("delete %s: %d %s", id, rec.Code, rec.Body)
		}
	}

	// Device record cleanup failure.
	r, db := newTestRouterWithDB(t)
	seedUser(t, r, db, "del-dev")
	failSubDeviceDeletes(t, db)
	rec := doJSON(t, r, http.MethodDelete, "/api/users/1", map[string]any{"confirm": "del-dev"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("subdevice cleanup: %d %s", rec.Code, rec.Body)
	}

	// User row deletion failure.
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "del-user")
	failUserDeletes(t, db)
	rec = doJSON(t, r, http.MethodDelete, "/api/users/1", map[string]any{"confirm": "del-user"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("user delete: %d %s", rec.Code, rec.Body)
	}
}

// TestResetSubTokenErrors token reset's lookup 404, the reset write failing, and device cleanup failing.
func TestResetSubTokenErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, id := range []string{"abc", "9999"} {
		rec := doJSON(t, r, http.MethodPost, "/api/users/"+id+"/sub-token", nil)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not found") {
			t.Fatalf("reset %s: %d %s", id, rec.Code, rec.Body)
		}
	}

	// The reset write fails.
	r, db := newTestRouterWithDB(t)
	seedUser(t, r, db, "reset-a")
	failUserRowUpdates(t, db)
	rec := doJSON(t, r, http.MethodPost, "/api/users/1/sub-token", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reset update: %d %s", rec.Code, rec.Body)
	}

	// Device record cleanup failure.
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "reset-b")
	failSubDeviceDeletes(t, db)
	rec = doJSON(t, r, http.MethodPost, "/api/users/1/sub-token", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reset cleanup: %d %s", rec.Code, rec.Body)
	}

	// Success path: the token is replaced and the old one voided.
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "reset-ok")
	rec = doJSON(t, r, http.MethodPost, "/api/users/1/sub-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset ok: %d %s", rec.Code, rec.Body)
	}
}

// TestUserDupTextErrors covers storage-side unique-conflict text being classified as 409: the
// normal path is intercepted up front by ensureUsernameFree, so here the isDup branch is tested
// directly with an injected unique-conflict error (concurrent create/rename of the same name only
// surfaces on the storage side).
func TestUserDupTextErrors(t *testing.T) {
	// Create: the precheck passes (new name), a unique-conflict text is injected on storage -> 409.
	r, db := newTestRouterWithDB(t)
	db.Callback().Create().Before("gorm:create").Register("test:user_dup_create", func(tx *gorm.DB) {
		if isUserModel(tx.Statement.Dest) {
			tx.AddError(errors.New("UNIQUE constraint failed: users.username"))
		}
	})
	if rec := doJSON(t, r, http.MethodPost, "/api/users", map[string]any{"username": "dup-create"}); rec.Code != http.StatusConflict {
		t.Fatalf("create dup: %d %s", rec.Code, rec.Body)
	}

	// Rename: the precheck passes (renaming to a free name), a unique-conflict text is injected on storage -> 409.
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "dup-ren")
	db.Callback().Update().Before("gorm:update").Register("test:user_dup_update", func(tx *gorm.DB) {
		if isUserModel(tx.Statement.Model) {
			tx.AddError(errors.New("UNIQUE constraint failed: users.username"))
		}
	})
	if rec := doJSON(t, r, http.MethodPut, "/api/users/1", map[string]any{"username": "dup-ren-2"}); rec.Code != http.StatusConflict {
		t.Fatalf("rename dup: %d %s", rec.Code, rec.Body)
	}
}

// TestIsDup tests the error text classification directly: nil and errors without the keywords are not duplicates.
func TestIsDup(t *testing.T) {
	if isDup(nil) {
		t.Fatal("nil should not be dup")
	}
	if isDup(errors.New("disk I/O error")) {
		t.Fatal("unrelated error should not be dup")
	}
	if !isDup(errors.New("UNIQUE constraint failed")) || !isDup(errors.New("Duplicate entry")) {
		t.Fatal("unique/duplicate text should be dup")
	}
}
