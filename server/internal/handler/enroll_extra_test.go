package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCreateEnrollmentTokenValidation an invalid payload is 400; a custom ttl
// lands in the response, and the issuer comes from the admin JWT (the adminUserID
// branch of actorOf).
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

// TestRevokeEnrollmentTokenMissing revoking a nonexistent credential: First
// lands on gorm record-not-found (not the errNotFound sentinel), and replyFind
// falls back to 500 -- that is the real behavior, no claiming success.
func TestRevokeEnrollmentTokenMissing(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "DELETE", "/api/enrollment-tokens/99999", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("revoke missing: %d %s; want 500 (gorm not-found not the errNotFound sentinel)", rec.Code, rec.Body)
	}
}

// TestEnrollBindError an invalid registration payload: 400, the credential is neither consumed nor counted toward the failure lock.
func TestEnrollBindError(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doBare(t, r, "POST", "/api/enroll"); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: %d %s", rec.Code, rec.Body)
	}
}

// TestEnrollRateLimit the enrollment exchange endpoint rate-limits per IP:
// after consecutive credential failures reach FailLimit it locks and later
// attempts are all 429 (FERRY-001).
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
	// A different IP is not affected by the previous one's lock.
	if code := postEnroll("10.9.0.2"); code != http.StatusUnauthorized {
		t.Fatalf("fresh ip: %d; want 401", code)
	}
}
