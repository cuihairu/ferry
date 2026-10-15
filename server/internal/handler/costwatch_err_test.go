package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// seedWatch 落一条关注行并返回其 id。
func seedWatch(t *testing.T, db *gorm.DB, provider, spec string) uint {
	t.Helper()
	w := storage.PriceWatch{Provider: provider, Region: "jp", Spec: spec,
		TargetPrice: 500, Enabled: true}
	if err := db.Create(&w).Error; err != nil {
		t.Fatal(err)
	}
	return w.ID
}

// TestCostWatchDBErrors 覆盖价格关注面取数与落库失败分支：列表、建档
// （非重复）、更新保存、删除事务、快照列表五处 500。
func TestCostWatchDBErrors(t *testing.T) {
	// 列表取数失败 → 500。
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:cwlist", db, (*[]storage.PriceWatch)(nil))
	if rec := doJSON(t, r, "GET", "/api/cost/watches", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	// 建档落库失败（非唯一冲突）→ 500。
	r2, db2 := newTestRouterWithDB(t)
	db2.Callback().Create().Before("gorm:create").Register("t:cwcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.PriceWatch); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r2, "POST", "/api/cost/watches", map[string]any{
		"provider": "vultr", "spec": "1m-1g",
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	// 更新保存失败 → 500（Save 走 Update 链）。
	r3, db3 := newTestRouterWithDB(t)
	id := seedWatch(t, db3, "vultr", "1m-1g")
	db3.Callback().Update().Before("gorm:update").Register("t:cwsave", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.PriceWatch); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r3, "PUT", "/api/cost/watches/"+itoa64(int(id)), map[string]any{
		"target_price": 600,
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	// 删除事务失败 → 500（级联清快照同事务，任一步失败整体回滚）。
	r4, db4 := newTestRouterWithDB(t)
	id4 := seedWatch(t, db4, "vultr", "2m-1g")
	db4.Callback().Delete().Before("gorm:delete").Register("t:cwdel", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.PriceWatch); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r4, "DELETE", "/api/cost/watches/"+itoa64(int(id4)), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	var left int64
	db4.Model(&storage.PriceWatch{}).Count(&left)
	if left != 1 {
		t.Fatalf("delete rollback lost rows: %d", left)
	}

	// 快照列表取数失败 → 500。
	r5, db5 := newTestRouterWithDB(t)
	id5 := seedWatch(t, db5, "dmit", "1m-1g")
	regQueryFail("t:cwsnap", db5, (*[]storage.PriceSnapshot)(nil))
	if rec := doJSON(t, r5, "GET", "/api/cost/watches/"+itoa64(int(id5))+"/snapshots", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("snapshots: %d %s", rec.Code, rec.Body)
	}
}
