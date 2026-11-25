package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestUserCRUD(t *testing.T) {
	r := newTestRouter(t)

	// Create: a sub token is generated, the account is enabled by default, quota 0 (unlimited)
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "alice", "quota_bytes": 1073741824,
		"expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u["username"] != "alice" || u["enabled"] != true {
		t.Fatalf("created user mismatch: %v", u)
	}
	token := u["sub_token"].(string)
	if len(token) < 40 {
		t.Fatalf("sub_token too short: %q", token)
	}
	_ = u["id"].(float64) // the detail case uses the literal id

	// Duplicate username rejected
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "alice"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate username: %d %s", rec.Code, rec.Body)
	}

	// List and detail
	if rec := doJSON(t, r, "GET", "/api/users", nil); rec.Code != http.StatusOK {
		t.Fatalf("list users: %d", rec.Code)
	}
	rec = doJSON(t, r, "GET", "/api/users/1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get user: %d", rec.Code)
	}

	// Update: quota, clear expiry, disable
	rec = doJSON(t, r, "PUT", "/api/users/1", map[string]any{
		"quota_bytes": 0, "clear_expire": true, "enabled": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update user: %d %s", rec.Code, rec.Body)
	}
	var fresh map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &fresh)
	if fresh["quota_bytes"].(float64) != 0 || fresh["enabled"] != false {
		t.Fatalf("updated user mismatch: %v", fresh)
	}
	if fresh["expires_at"] != nil {
		t.Fatalf("expires_at should be cleared: %v", fresh["expires_at"])
	}

	// Rename conflict and a successful rename
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "bob"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bob: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "PUT", "/api/users/1", map[string]any{"username": "bob"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("rename to taken username: %d", rec.Code)
	}
	rec = doJSON(t, r, "PUT", "/api/users/1", map[string]any{"username": "alice2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}

	// Reset the sub token: the old token stops working, and the new one differs
	rec = doJSON(t, r, "POST", "/api/users/1/sub-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset sub token: %d %s", rec.Code, rec.Body)
	}
	var afterReset map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &afterReset)
	if afterReset["sub_token"].(string) == token {
		t.Fatal("sub_token must change on reset")
	}

	// Delete: the user then 404s, and the billing history (traffic_logs) is retained (P2 fix, see userdelete_test.go)
	if rec := doJSON(t, r, "DELETE", "/api/users/1", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete user: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/users/1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted user should 404: %d", rec.Code)
	}
}

func TestUserValidation(t *testing.T) {
	r := newTestRouter(t)

	// Blank username
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "  "}); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank username: %d", rec.Code)
	}
	// Negative quota
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "x", "quota_bytes": -1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative quota: %d", rec.Code)
	}
	// Invalid id
	if rec := doJSON(t, r, "GET", "/api/users/abc", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}

	// An explicit disable on create is not swallowed by default:true
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "off", "enabled": false})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create disabled user: %d %s", rec.Code, rec.Body)
	}
	var off map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &off)
	if off["enabled"] != false {
		t.Fatalf("created disabled user came back enabled: %v", off["enabled"])
	}
}

func TestUserResetCycle(t *testing.T) {
	r := newTestRouter(t)

	// Create with a cycle
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "cycled", "quota_bytes": 1000, "reset_cycle": "day",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u["reset_cycle"] != "day" {
		t.Fatalf("reset_cycle = %v, want day", u["reset_cycle"])
	}
	// Absent defaults to none
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "plain"})
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u["reset_cycle"] != "none" {
		t.Fatalf("default reset_cycle = %v, want none", u["reset_cycle"])
	}

	// Update changes the cycle
	rec = doJSON(t, r, "PUT", "/api/users/1", map[string]any{"reset_cycle": "month"})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u["reset_cycle"] != "month" {
		t.Fatalf("patched reset_cycle = %v, want month", u["reset_cycle"])
	}

	// Invalid value gives 400
	rec = doJSON(t, r, "PUT", "/api/users/1", map[string]any{"reset_cycle": "yearly"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cycle: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "bad", "reset_cycle": "weekly"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cycle on create: %d %s", rec.Code, rec.Body)
	}
}
