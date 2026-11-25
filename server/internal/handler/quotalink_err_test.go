package handler

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestQuotaLinkDBErrors covers read failures on the linkage admin side: a failed
// setting read and a failed trail list query both return 500; a failed reread
// after a successful save also returns 500 (the save already succeeded, so no
// success is echoed).
func TestQuotaLinkDBErrors(t *testing.T) {
	// setting read failure -> 500.
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:ql_setting", db, (*storage.Setting)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/quota-link", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("load setting: %d %s", rec.Code, rec.Body)
	}

	// trail list query failure -> 500.
	r, db = newTestRouterWithDB(t)
	regQueryFail("t:ql_rows", db, (*[]storage.QuotaAction)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/quota-link", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list rows: %d %s", rec.Code, rec.Body)
	}

	// reread failure after a successful save -> 500.
	r, db = newTestRouterWithDB(t)
	var saved atomic.Bool
	db.Callback().Update().Before("gorm:update").Register("t:ql_save", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Setting); ok {
			saved.Store(true)
		}
	})
	db.Callback().Query().Before("gorm:query").Register("t:ql_reload", func(tx *gorm.DB) {
		if !saved.Load() {
			return
		}
		if _, ok := tx.Statement.Dest.(*storage.Setting); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	rec := doJSON(t, r, http.MethodPut, "/api/quota-link", map[string]any{
		"enabled": true, "traffic_percent": 95,
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reload setting: %d %s", rec.Code, rec.Body)
	}
}

// TestQuotaLinkBadPayload covers a bad PUT payload returning 400 (rejected before
// it reaches the database).
func TestQuotaLinkBadPayload(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doRaw(t, r, http.MethodPut, "/api/quota-link", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad payload: %d %s", rec.Code, rec.Body)
	}
}
