package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestEntryDomainsDBErrors 覆盖清单取数失败与建/改/删三处落库失败均 500。
func TestEntryDomainsDBErrors(t *testing.T) {
	// 清单查询失败 → 500。
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:ed_list", db, (*[]storage.EntryDomain)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/entry-domains", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	// 建库落库失败 → 500。
	r, db = newTestRouterWithDB(t)
	db.Callback().Create().Before("gorm:create").Register("t:ed_create", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.EntryDomain); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, http.MethodPost, "/api/entry-domains", map[string]any{
		"domain": "a.com", "role": "primary",
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	// 改库保存失败（Save 走 Update 链）→ 500。
	r, db = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/entry-domains", map[string]any{
		"domain": "b.com", "role": "primary",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed create: %d %s", rec.Code, rec.Body)
	}
	db.Callback().Update().Before("gorm:update").Register("t:ed_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.EntryDomain); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, http.MethodPut, "/api/entry-domains/1", map[string]any{"enabled": false}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	// 删库删除失败 → 500（与 404 区分）。
	r, db = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/entry-domains", map[string]any{
		"domain": "c.com", "role": "backup",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("seed create: %d %s", rec.Code, rec.Body)
	}
	db.Callback().Delete().Before("gorm:delete").Register("t:ed_delete", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.EntryDomain); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, http.MethodDelete, "/api/entry-domains/1", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}
