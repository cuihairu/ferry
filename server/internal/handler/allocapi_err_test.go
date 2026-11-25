package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestAllocDBErrors covers assignment-plane fetch failures: a failed policy
// read and a failed list of effective auto rows both return 500.
func TestAllocDBErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:alloc_setting", db, (*storage.Setting)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/alloc", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("load policy: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	regQueryFail("t:alloc_rows", db, (*[]storage.LandingAssignment)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/alloc", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list rows: %d %s", rec.Code, rec.Body)
	}
}

// TestAllocPolicyBadPayload covers a 400 for a bad PUT policy payload
// (rejected before it hits the DB).
func TestAllocPolicyBadPayload(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doRaw(t, r, http.MethodPut, "/api/alloc/policy", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad payload: %d %s", rec.Code, rec.Body)
	}
}
