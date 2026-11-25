package handler

import (
	"net/http"
	"testing"
)

// TestTransportStatusWindowOK covers the valid judge window taking effect (data fetch succeeds within 30-3600 seconds).
func TestTransportStatusWindowOK(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodGet, "/api/transport-status?window_sec=120", nil); rec.Code != http.StatusOK {
		t.Fatalf("window_sec=120: %d %s", rec.Code, rec.Body)
	}
}

// TestTransportStatusDBError covers a data fetch failure during region judgment returning 500.
func TestTransportStatusDBError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/transport-status?window_sec=60", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("judge fail: %d %s", rec.Code, rec.Body)
	}
}
