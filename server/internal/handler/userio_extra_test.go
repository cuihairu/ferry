package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestImportBadResetCycle: an import row carries an invalid reset cycle: the
// whole batch is rejected and the legal values are stated.
func TestImportBadResetCycle(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	body := `{"users":[{"username":"bad-cycle","reset_cycle":"bogus"}]}`
	req := httptest.NewRequest("POST", "/api/users/import", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "reset_cycle must be none/day/week/month") {
		t.Fatalf("bad reset_cycle: %d %s", rec.Code, rec.Body)
	}
}
