package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestFeedXMLDBError 覆盖公告取数失败 500（公开订阅面无凭据）。
func TestFeedXMLDBError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:feed_notif", db, (*[]storage.Notification)(nil))
	if rec := doBare(t, r, http.MethodGet, "/feed.xml"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("feed fail: %d %s", rec.Code, rec.Body)
	}
}
