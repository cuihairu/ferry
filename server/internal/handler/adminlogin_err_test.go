package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

// TestAdminLoginGuards covers the admin login error surface: rate-limit lockout 429,
// bad payload 400, unknown user and non-admin 401.
func TestAdminLoginGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// Bad payload -> 400.
	if rec := doAdmin(t, r, "POST", "/admin/login", []byte("{bad")); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}

	// Unknown user -> 401 (uniform wording to prevent enumeration).
	rec := doPanel(t, r, "", "POST", "/admin/login", map[string]any{"username": "ghost", "password": "x"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user: %d %s", rec.Code, rec.Body)
	}

	// A valid account that is disabled or lacks the admin flag -> 401.
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

	// Locked after 5 failures inside the window -> 429.
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

// TestAdminLoginDBErrors covers the unified 401 response (no leaked DB error) when user lookup fails.
func TestAdminLoginDBErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdmin(t, db, "root-err", "pw-root")
	regQueryFail("t:admfind", db, (*storage.User)(nil))
	rec := doPanel(t, r, "", "POST", "/admin/login", map[string]any{"username": "root-err", "password": "pw-root"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("db fail: %d %s", rec.Code, rec.Body)
	}
}

// TestLoginRedeemLimiterIsolation covers P2 cross-surface isolation: credential login
// and redemption use independent limiters -- a login brute force (loginLimiter locked
// after 5 failures) does not lock redemption, and a redemption brute force does not
// lock login. Previously both shared redeemLimiter, so hammering either surface
// caused a cross-surface DoS on the other.
func TestLoginRedeemLimiterIsolation(t *testing.T) {
	// Surface 1: redemption is unaffected after a login brute force.
	r, _ := newTestRouterWithDB(t)
	for i := 0; i < 5; i++ {
		rec := doPanel(t, r, "", "POST", "/admin/login", map[string]any{"username": "ghost", "password": "x"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("login fail #%d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	if rec := doPanel(t, r, "", "POST", "/admin/login", map[string]any{"username": "ghost", "password": "x"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("login after brute: %d, want 429", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": "NOPE-0000", "user_id": 1}); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("redeem cross-locked by login brute: %d", rec.Code)
	}

	// Surface 2: login is unaffected after a redemption brute force.
	r2, _ := newTestRouterWithDB(t)
	for i := 0; i < 5; i++ {
		rec := doJSON(t, r2, "POST", "/api/redeem", map[string]any{"code": "NOPE-0000", "user_id": 1})
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("redeem prematurely locked #%d", i+1)
		}
	}
	if rec := doJSON(t, r2, "POST", "/api/redeem", map[string]any{"code": "NOPE-0000", "user_id": 1}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("redeem after brute: %d, want 429", rec.Code)
	}
	if rec := doPanel(t, r2, "", "POST", "/admin/login", map[string]any{"username": "ghost", "password": "x"}); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("login cross-locked by redeem brute: %d", rec.Code)
	}
}
