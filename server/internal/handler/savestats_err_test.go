package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSaveStatsDBErrors covers two fetch-failure 500s: daily stat rows and node mapping.
func TestSaveStatsDBErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:save_stats", db, (*[]storage.SaveStat)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/save-stats?days=7", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("stats fail: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	regQueryFail("t:save_nodes", db, (*[]storage.Node)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/save-stats?days=7", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("nodes fail: %d %s", rec.Code, rec.Body)
	}
}
