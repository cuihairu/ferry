package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 三十六批补口：卡密批次（PAY-3）错误口径——建号坏绑定/负价/过期时间已过/
// 事务内批次落库失败/卡密落库重复冲突 409 与库故障 500、列表取数失败/剩余
// 聚合失败、卡密列表失败、导出查无批次与取数失败、禁用定位失败/更新失败/
// 回读失败、删除查无与落删失败、findCardBatch 坏 id 与库故障。
// 留白：cardcode.Generate 的 crypto/rand 错误（n<=0 已被前置校验拦下，正
// 常流程不可达）。

// validCardBatchBody 是满足全部校验的建批载荷。
func validCardBatchBody(name string) map[string]any {
	return map[string]any{"name": name, "grant_type": "add_quota",
		"grant_value": 1 << 30, "price_cents": 1000, "total": 2, "created_by": "admin"}
}

// seedCardCodeRow 直落一张未用卡密。
func seedCardCodeRow(t *testing.T, db *gorm.DB, batchID uint, code string) {
	t.Helper()
	if err := db.Create(&storage.CardCode{BatchID: batchID, Code: code, Status: "unused"}).Error; err != nil {
		t.Fatalf("seed code: %v", err)
	}
}

// TestCreateCardBatchErrors 建批：坏绑定、负价、过期时间已过、事务内批次落
// 库失败、卡密落库重复冲突 409 与库故障 500。
func TestCreateCardBatchErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/card-batches", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPost, "/api/card-batches",
		map[string]any{"name": "p", "grant_type": "add_quota", "grant_value": 100,
			"price_cents": -1, "total": 1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative price: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPost, "/api/card-batches",
		map[string]any{"name": "p", "grant_type": "add_quota", "grant_value": 100,
			"price_cents": 10, "total": 1, "expired_at": "2020-01-01T00:00:00Z"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("past expiry: %d %s", rec.Code, rec.Body)
	}

	// 事务内批次落库失败（首个 create 语句注错）。
	r, db := newTestRouterWithDB(t)
	failCreateOn(t, db, "test:cardbatch_create", isCardBatchModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/card-batches", validCardBatchBody("tx-b")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("batch create: %d %s", rec.Code, rec.Body)
	}

	// 卡密落库重复冲突：isDup 认 duplicate 文案 → 409。
	r, db = newTestRouterWithDB(t)
	db.Callback().Create().Before("gorm:create").Register("test:cardcode_dup", func(tx *gorm.DB) {
		if isCardCodeModel(tx.Statement.Dest) || isCardCodeModel(tx.Statement.Model) {
			tx.AddError(errors.New("duplicate card code"))
		}
	})
	if rec := doJSON(t, r, http.MethodPost, "/api/card-batches", validCardBatchBody("dup-b")); rec.Code != http.StatusConflict {
		t.Fatalf("dup codes: %d %s", rec.Code, rec.Body)
	}

	// 卡密落库普通故障 → 500。
	r, db = newTestRouterWithDB(t)
	failCreateOn(t, db, "test:cardcode_create", isCardCodeModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/card-batches", validCardBatchBody("gen-b")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("codes create: %d %s", rec.Code, rec.Body)
	}
}

// TestListCardBatchesErrors 批次列表：取数失败与剩余聚合失败。
func TestListCardBatchesErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:cardbatch_q", isCardBatchModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/card-batches", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}

	// 剩余聚合（GROUP BY Scan 走 gorm:row）注错。
	r, db = newTestRouterWithDB(t)
	seedSellableBatch(t, db, "agg-b", 1000)
	failRowOn(t, db, "test:cardcode_row", isCardCodeModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/card-batches", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("agg: %d %s", rec.Code, rec.Body)
	}
}

// TestListCardCodesErrors 卡密列表取数失败 500。
func TestListCardCodesErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	batchID := seedSellableBatch(t, db, "c-list", 1000)
	failQueryOn(t, db, "test:cardcode_q", isCardCodeModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/card-batches/"+strconv.FormatUint(uint64(batchID), 10)+"/codes", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list codes: %d %s", rec.Code, rec.Body)
	}
}

// TestExportCardBatchErrors 导出：坏 id 短路、取数失败 500。
func TestExportCardBatchErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodGet, "/api/card-batches/abc/export.csv", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodGet, "/api/card-batches/9999/export.csv", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	batchID := seedSellableBatch(t, db, "exp-b", 1000)
	failQueryOn(t, db, "test:cardcode_q", isCardCodeModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/card-batches/"+strconv.FormatUint(uint64(batchID), 10)+"/export.csv", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
}

// TestDisableCardCodeErrors 禁用：定位失败、更新失败与回读失败（回读是
// 第二道查询，用计数定向）。
func TestDisableCardCodeErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedSellableBatch(t, db, "dis-b", 1000)
	failQueryOn(t, db, "test:cardcode_q", isCardCodeModel)
	if rec := doJSON(t, r, http.MethodPatch, "/api/card-codes/1/disable", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("locate: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	batchID := seedSellableBatch(t, db, "upd-b", 1000)
	seedCardCodeRow(t, db, batchID, "AAA-BBB-CCC-DDD")
	failUpdateOn(t, db, "test:cardcode_update", isCardCodeModel)
	if rec := doJSON(t, r, http.MethodPatch, "/api/card-codes/1/disable", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	// 回读失败：定位（第 1 查）放行，回读（第 2 查）注错。
	r, db = newTestRouterWithDB(t)
	batchID = seedSellableBatch(t, db, "read-b", 1000)
	seedCardCodeRow(t, db, batchID, "EEE-FFF-GGG-HHH")
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:cardcode_reread", func(tx *gorm.DB) {
		if isCardCodeModel(tx.Statement.Model) || isCardCodeModel(tx.Statement.Dest) {
			seen++
			if seen == 2 {
				tx.AddError(errors.New("db down"))
			}
		}
	})
	if rec := doJSON(t, r, http.MethodPatch, "/api/card-codes/1/disable", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("reread: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteCardBatchErrors 删除：查无短路与落删失败。
func TestDeleteCardBatchErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/card-batches/9999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	batchID := seedSellableBatch(t, db, "del-b", 1000)
	db.Callback().Delete().Before("gorm:delete").Register("test:cardbatch_delete", func(tx *gorm.DB) {
		if isCardBatchModel(tx.Statement.Model) || isCardBatchModel(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, http.MethodDelete, "/api/card-batches/"+strconv.FormatUint(uint64(batchID), 10), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}

// TestFindCardBatchDBFail findCardBatch 库故障定位：First 落 500（非记
// 录不存在不走 404）。
func TestFindCardBatchDBFail(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	closeSQL(t, db)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Params = gin.Params{{Key: "id", Value: "9999"}}
	if _, ok := h.findCardBatch(c); ok {
		t.Fatal("closed db: want !ok")
	}
}
