package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestFeedXMLDBError covers the announcement fetch failure 500 (the public feed route needs no credential).
func TestFeedXMLDBError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:feed_notif", db, (*[]storage.Notification)(nil))
	if rec := doBare(t, r, http.MethodGet, "/feed.xml"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("feed fail: %d %s", rec.Code, rec.Body)
	}
}
