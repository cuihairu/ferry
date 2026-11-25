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

// Batch 36 gap-filling: error paths for card code batches (PAY-3) — create
// with bad binding/negative price/past expiry/batch persist failure inside the
// transaction/card code persist duplicate conflict 409 and DB fault 500, list
// fetch failure and remaining-aggregate failure, code listing failure, export
// batch-not-found and fetch failure, disable locate failure/update failure/
// re-read failure, delete not-found and persist failure, and findCardBatch
// with a bad id and a DB fault.
// Gaps left: the crypto/rand error inside cardcode.Generate (n<=0 is already
// blocked by the pre-check, so it is unreachable in the normal flow).

// validCardBatchBody is a create-batch payload satisfying every validation.
func validCardBatchBody(name string) map[string]any {
	return map[string]any{"name": name, "grant_type": "add_quota",
		"grant_value": 1 << 30, "price_cents": 1000, "total": 2, "created_by": "admin"}
}

// seedCardCodeRow inserts one unused card code directly.
func seedCardCodeRow(t *testing.T, db *gorm.DB, batchID uint, code string) {
	t.Helper()
	if err := db.Create(&storage.CardCode{BatchID: batchID, Code: code, Status: "unused"}).Error; err != nil {
		t.Fatalf("seed code: %v", err)
	}
}

// TestCreateCardBatchErrors: create batch — bad binding, negative price, past
// expiry, batch persist failure inside the transaction, and card code persist
// duplicate conflict 409 with DB fault 500.
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

	// batch persist failure inside the transaction (error injected on the first create statement).
	r, db := newTestRouterWithDB(t)
	failCreateOn(t, db, "test:cardbatch_create", isCardBatchModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/card-batches", validCardBatchBody("tx-b")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("batch create: %d %s", rec.Code, rec.Body)
	}

	// card code persist duplicate conflict: isDup recognizes the duplicate wording -> 409.
	r, db = newTestRouterWithDB(t)
	db.Callback().Create().Before("gorm:create").Register("test:cardcode_dup", func(tx *gorm.DB) {
		if isCardCodeModel(tx.Statement.Dest) || isCardCodeModel(tx.Statement.Model) {
			tx.AddError(errors.New("duplicate card code"))
		}
	})
	if rec := doJSON(t, r, http.MethodPost, "/api/card-batches", validCardBatchBody("dup-b")); rec.Code != http.StatusConflict {
		t.Fatalf("dup codes: %d %s", rec.Code, rec.Body)
	}

	// ordinary card code persist fault -> 500.
	r, db = newTestRouterWithDB(t)
	failCreateOn(t, db, "test:cardcode_create", isCardCodeModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/card-batches", validCardBatchBody("gen-b")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("codes create: %d %s", rec.Code, rec.Body)
	}
}

// TestListCardBatchesErrors: batch list — fetch failure and remaining-aggregate failure.
func TestListCardBatchesErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:cardbatch_q", isCardBatchModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/card-batches", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}

	// Inject an error into the remaining aggregate (GROUP BY Scan goes through gorm:row).
	r, db = newTestRouterWithDB(t)
	seedSellableBatch(t, db, "agg-b", 1000)
	failRowOn(t, db, "test:cardcode_row", isCardCodeModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/card-batches", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("agg: %d %s", rec.Code, rec.Body)
	}
}

// TestListCardCodesErrors: card code listing fetch failure returns 500.
func TestListCardCodesErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	batchID := seedSellableBatch(t, db, "c-list", 1000)
	failQueryOn(t, db, "test:cardcode_q", isCardCodeModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/card-batches/"+strconv.FormatUint(uint64(batchID), 10)+"/codes", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list codes: %d %s", rec.Code, rec.Body)
	}
}

// TestExportCardBatchErrors: export — bad id short-circuit, fetch failure 500.
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

// TestDisableCardCodeErrors: disable — locate failure, update failure, and
// re-read failure (the re-read is the second query, targeted by counting).
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

	// Re-read failure: let the locate query (1st) pass and inject the error into the re-read (2nd query).
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

// TestDeleteCardBatchErrors: delete — not-found short-circuit and persist failure.
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

// TestFindCardBatchDBFail: findCardBatch locate failure under a DB fault —
// First lands as 500 (not "record not found", so it does not take the 404 path).
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
