package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestImportBadResetCycle 导入行带非法重置周期：整批拒绝并说明合法取值。
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
