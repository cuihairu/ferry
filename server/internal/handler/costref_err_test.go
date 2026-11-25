package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCostRefDBErrors covers the data-fetch failure branches of the reference-price endpoints: source read (settings query)
// failure → 500 for both probe and batch reconciliation under the same contract, batch reconciliation node fetch failure → 500, bad import payload → 400.
func TestCostRefDBErrors(t *testing.T) {
	// Probe: source read fails → 500.
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:refsetting", db, (*storage.Setting)(nil))
	if rec := doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&spec=100m", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("probe source: %d %s", rec.Code, rec.Body)
	}

	// Batch reconciliation: source read fails → 500.
	r2, db2 := newTestRouterWithDB(t)
	regQueryFail("t:refsetting2", db2, (*storage.Setting)(nil))
	if rec := doJSON(t, r2, "GET", "/api/cost/ref-check", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("check source: %d %s", rec.Code, rec.Body)
	}

	// Batch reconciliation: node fetch fails → 500 (import a table first so the source is non-empty).
	r3, db3 := newTestRouterWithDB(t)
	if rec := doJSON(t, r3, "PUT", "/api/cost/ref-table", map[string]any{"table": refTestTable}); rec.Code != http.StatusOK {
		t.Fatalf("put table: %d %s", rec.Code, rec.Body)
	}
	regQueryFail("t:refnodes", db3, (*[]storage.Node)(nil))
	if rec := doJSON(t, r3, "GET", "/api/cost/ref-check", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("nodes fail: %d %s", rec.Code, rec.Body)
	}

	// Import: bad payload → 400.
	if rec := doRaw(t, r3, "PUT", "/api/cost/ref-table", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
}
