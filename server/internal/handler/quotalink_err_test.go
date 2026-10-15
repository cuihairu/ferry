package handler

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestQuotaLinkDBErrors 覆盖联动管理端取数故障：设置读取失败与留痕列表
// 查询失败均 500；保存成功后回读失败同样 500（保存已成功，不回显成功）。
func TestQuotaLinkDBErrors(t *testing.T) {
	// 设置读取失败 → 500。
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:ql_setting", db, (*storage.Setting)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/quota-link", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("load setting: %d %s", rec.Code, rec.Body)
	}

	// 留痕列表查询失败 → 500。
	r, db = newTestRouterWithDB(t)
	regQueryFail("t:ql_rows", db, (*[]storage.QuotaAction)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/quota-link", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list rows: %d %s", rec.Code, rec.Body)
	}

	// 保存成功后再回读失败 → 500。
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

// TestQuotaLinkBadPayload 覆盖 PUT 坏载荷 400（落库前拦截）。
func TestQuotaLinkBadPayload(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doRaw(t, r, http.MethodPut, "/api/quota-link", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad payload: %d %s", rec.Code, rec.Body)
	}
}
