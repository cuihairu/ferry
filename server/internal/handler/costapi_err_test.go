package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCostAPIDBError covers a failed data fetch for the cost dashboard: building
// the month-to-date report depends on reading settings, so injecting a settings
// query failure → 500.
func TestCostAPIDBError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:cost_setting", db, (*storage.Setting)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/cost", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("build report: %d %s", rec.Code, rec.Body)
	}
}

// TestCostThresholdGuards covers the two validation gates on threshold setting: a bad payload and a missing cents both → 400.
func TestCostThresholdGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doRaw(t, r, http.MethodPut, "/api/cost/threshold", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad payload: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/cost/threshold", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing cents: %d %s", rec.Code, rec.Body)
	}
}
