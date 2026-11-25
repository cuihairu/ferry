package handler

import (
	"net/http"
	"testing"
)

// TestOutageSetFail covers a failing outage-flag save 500 (SetSetting runs
// Save's Update chain; error injection is aimed by the settings save index).
func TestOutageSetFail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failNthSettingSave("t:outage_set", db, 1)
	if rec := doJSON(t, r, http.MethodPut, "/api/outage", map[string]any{"enabled": true}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("set outage: %d %s", rec.Code, rec.Body)
	}
}
