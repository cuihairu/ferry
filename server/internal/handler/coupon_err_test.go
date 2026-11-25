package handler

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch 37 coverage: coupon (PROMO-1) error contracts — list fetch failure, validateDiscount
// rules min_amount out of range / targeted batch count failure / bad scope shape, create with default per_user
// landing on 1 / persist failure, update with bad id / db-fault locate / bad bind / validation failure / save
// failure, delete with bad id / delete failure, couponCheck locate failure / per-user count failure, applyCouponTx validation
// passthrough plus conditional redemption reporting zero rows as exhausted / successful redemption discount.
// Two gaps left: the crypto/rand error in randomCode8 (rand.Read has a primary entropy source and always succeeds);
// applyCouponTx's RowsAffected==0 redemption backstop branch — its update condition
// total=0 OR used<total is shaped identically to couponCheck's rejection condition, and in a serial flow the read is the
// write condition, so a non-concurrent row edit is unreachable; same shape of defensive code as panel 358.

// isCouponModel reports whether the statement targets the coupon table.
func isCouponModel(v any) bool {
	switch v.(type) {
	case *storage.Coupon, []storage.Coupon, *[]storage.Coupon:
		return true
	}
	return false
}

// seedCouponRow inserts a coupon row directly (scope always all) and returns its ID.
func seedCouponRow(t *testing.T, db *gorm.DB, code, kind string, value, total, used, perUser int64) uint {
	t.Helper()
	row := storage.Coupon{Code: code, Kind: kind, Value: value, Scope: "all",
		Total: total, Used: used, PerUser: perUser, CreatedAt: time.Now()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed coupon %s: %v", code, err)
	}
	return row.ID
}

// TestListCouponsError list fetch failure → 500.
func TestListCouponsError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:coupon_q", isCouponModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/coupons", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestValidateDiscountRulesErrors direct rule validation: min_amount out of range, targeted batch
// count failure and bad scope shape.
func TestValidateDiscountRulesErrors(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	if _, err := h.validateDiscountRules("cut", 10, "", -1); err == nil ||
		!containsStr(err.Error(), "min_amount") {
		t.Fatalf("min amount: %v", err)
	}

	// Targeted batch existence count fails (Count lands on the CardBatch table).
	failQueryOn(t, db, "test:cardbatch_q", isCardBatchModel)
	if _, err := h.validateDiscountRules("cut", 10, "batch:1", 0); err == nil {
		t.Fatalf("scope count: want err")
	}

	// Bad scope shape (non-numeric after batch:).
	if _, err := h.validateDiscountRules("cut", 10, "batch:abc", 0); err == nil ||
		!containsStr(err.Error(), "scope must be all") {
		t.Fatalf("bad scope: %v", err)
	}
}

// TestCreateCouponErrors create: bad bind, default per_user landing on 1, and persist failure → 500.
func TestCreateCouponErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/coupons", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	body := map[string]any{"code": "PD1", "kind": "cut", "value": 10,
		"min_amount": 0, "total": 0, "per_user": 0}
	if rec := doJSON(t, r, http.MethodPost, "/api/coupons", body); rec.Code != http.StatusCreated ||
		!containsStr(rec.Body.String(), `"per_user":1`) {
		t.Fatalf("per_user default: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	failCreateOn(t, db, "test:coupon_create", isCouponModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/coupons", body); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateCouponErrors update: bad id, db-fault locate, bad bind, validation failure and save
// failure.
func TestUpdateCouponErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/coupons/abc",
		map[string]any{"code": "U1", "kind": "cut", "value": 10}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedCouponRow(t, db, "U1", "cut", 10, 0, 0, 0)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/coupons/1",
		map[string]any{"code": "U1", "kind": "cut", "value": 10}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("locate: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedCouponRow(t, db, "U2", "cut", 10, 0, 0, 0)
	if rec := doJSON(t, r, http.MethodPut, "/api/coupons/1", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/coupons/1",
		map[string]any{"code": "U2", "kind": "weird", "value": 10}); rec.Code != http.StatusBadRequest {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedCouponRow(t, db, "U3", "cut", 10, 0, 0, 0)
	failUpdateOn(t, db, "test:coupon_save", isCouponModel)
	if rec := doJSON(t, r, http.MethodPut, "/api/coupons/1",
		map[string]any{"code": "U3", "kind": "cut", "value": 10}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteCouponErrors delete: bad id and delete failure.
func TestDeleteCouponErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/coupons/abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedCouponRow(t, db, "D1", "cut", 10, 0, 0, 0)
	db.Callback().Delete().Before("gorm:delete").Register("test:coupon_delete", func(tx *gorm.DB) {
		if isCouponModel(tx.Statement.Model) || isCouponModel(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, http.MethodDelete, "/api/coupons/1", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}

// TestCouponCheckDBFails couponCheck direct test: locate failure and per-user count failure.
func TestCouponCheckDBFails(t *testing.T) {
	// Locate failure (First goes through gorm:query).
	_, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:coupon_first", isCouponModel)
	if _, err := couponCheck(db, "NOPE", &storage.User{}, &storage.CardBatch{}, 1000, time.Now()); err == nil {
		t.Fatal("locate: want err")
	}

	// Per-user count failure (Count lands on the PaymentOrder table).
	_, db = newTestRouterWithDB(t)
	seedCouponRow(t, db, "CP1", "cut", 10, 0, 0, 0)
	failQueryOn(t, db, "test:payorder_count", isPayOrderModel)
	if _, err := couponCheck(db, "CP1", &storage.User{}, &storage.CardBatch{}, 1000, time.Now()); err == nil {
		t.Fatal("per-user count: want err")
	}
}

// TestApplyCouponTxErrors applyCouponTx direct test: validation passthrough, conditional
// redemption reporting zero rows as exhausted, and successful redemption discount.
func TestApplyCouponTxErrors(t *testing.T) {
	// Validation passthrough (no such code → invalid coupon).
	_, db := newTestRouterWithDB(t)
	if _, _, err := applyCouponTx(db, "NOPE", &storage.User{}, &storage.CardBatch{}, 1000, time.Now()); err == nil {
		t.Fatal("passthrough: want err")
	}

	// Conditional redemption affecting zero rows (used already at total) reports exhausted.
	_, db = newTestRouterWithDB(t)
	seedCouponRow(t, db, "CP2", "cut", 10, 1, 1, 0)
	_, _, err := applyCouponTx(db, "CP2", &storage.User{}, &storage.CardBatch{}, 1000, time.Now())
	if err == nil || !containsStr(err.Error(), "已领完") {
		t.Fatalf("burn exhausted: %v", err)
	}

	// Successful redemption: the cut brings the amount paid to 990 and used+1.
	_, db = newTestRouterWithDB(t)
	seedCouponRow(t, db, "CP3", "cut", 10, 0, 0, 0)
	paid, _, err := applyCouponTx(db, "CP3", &storage.User{}, &storage.CardBatch{}, 1000, time.Now())
	if err != nil || paid != 990 {
		t.Fatalf("burn ok: paid=%d err=%v", paid, err)
	}
	var cp storage.Coupon
	if err := db.First(&cp, "code = ?", "CP3").Error; err != nil || cp.Used != 1 {
		t.Fatalf("burn used: %+v err=%v", cp, err)
	}
}
