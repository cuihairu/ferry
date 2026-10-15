package handler

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 三十七批补口：优惠码（PROMO-1）错误口径——列表取数失败、validateDiscount
// 规则 min_amount 越界/定向批次计数失败/坏 scope 形态、建号缺省 per_user
// 落 1/落库失败、更新坏 id/库故障定位/坏绑定/校验失败/保存失败、删除坏
// id/落删失败、couponCheck 定位失败/每用户计数失败、applyCouponTx 校验
// 不过 passthrough 与条件核销零行报已领完/核销成功减额。
// 留白 2 处：randomCode8 的 crypto/rand 错误（rand.Read 有主熵源恒成功）；
// applyCouponTx 的 RowsAffected==0 核销兜底分支——其更新条件
// total=0 OR used<total 与 couponCheck 的拒绝条件同形，串行流下读数即写
// 条件，非并发改行不可达，与 panel 358 同型防御性代码。

// isCouponModel 判断语句是否落在优惠码表上。
func isCouponModel(v any) bool {
	switch v.(type) {
	case *storage.Coupon, []storage.Coupon, *[]storage.Coupon:
		return true
	}
	return false
}

// seedCouponRow 直落一行优惠码（scope 恒 all），返回其 ID。
func seedCouponRow(t *testing.T, db *gorm.DB, code, kind string, value, total, used, perUser int64) uint {
	t.Helper()
	row := storage.Coupon{Code: code, Kind: kind, Value: value, Scope: "all",
		Total: total, Used: used, PerUser: perUser, CreatedAt: time.Now()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed coupon %s: %v", code, err)
	}
	return row.ID
}

// TestListCouponsError 列表取数失败 500。
func TestListCouponsError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:coupon_q", isCouponModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/coupons", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestValidateDiscountRulesErrors 校验规则直测：min_amount 越界、定向批
// 次计数失败与坏 scope 形态。
func TestValidateDiscountRulesErrors(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	if _, err := h.validateDiscountRules("cut", 10, "", -1); err == nil ||
		!containsStr(err.Error(), "min_amount") {
		t.Fatalf("min amount: %v", err)
	}

	// 定向批次存在性计数失败（Count 落在 CardBatch 表）。
	failQueryOn(t, db, "test:cardbatch_q", isCardBatchModel)
	if _, err := h.validateDiscountRules("cut", 10, "batch:1", 0); err == nil {
		t.Fatalf("scope count: want err")
	}

	// 坏 scope 形态（batch: 后非数字）。
	if _, err := h.validateDiscountRules("cut", 10, "batch:abc", 0); err == nil ||
		!containsStr(err.Error(), "scope must be all") {
		t.Fatalf("bad scope: %v", err)
	}
}

// TestCreateCouponErrors 建号：坏绑定、缺省 per_user 落 1 与落库失败 500。
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

// TestUpdateCouponErrors 更新：坏 id、库故障定位、坏绑定、校验失败与保存
// 失败。
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

// TestDeleteCouponErrors 删除：坏 id 与落删失败。
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

// TestCouponCheckDBFails couponCheck 直测：定位失败与每用户计数失败。
func TestCouponCheckDBFails(t *testing.T) {
	// 定位失败（First 走 gorm:query）。
	_, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:coupon_first", isCouponModel)
	if _, err := couponCheck(db, "NOPE", &storage.User{}, &storage.CardBatch{}, 1000, time.Now()); err == nil {
		t.Fatal("locate: want err")
	}

	// 每用户计数失败（Count 落在 PaymentOrder 表）。
	_, db = newTestRouterWithDB(t)
	seedCouponRow(t, db, "CP1", "cut", 10, 0, 0, 0)
	failQueryOn(t, db, "test:payorder_count", isPayOrderModel)
	if _, err := couponCheck(db, "CP1", &storage.User{}, &storage.CardBatch{}, 1000, time.Now()); err == nil {
		t.Fatal("per-user count: want err")
	}
}

// TestApplyCouponTxErrors applyCouponTx 直测：校验不过 passthrough、条件
// 核销零行报已领完与核销成功减额。
func TestApplyCouponTxErrors(t *testing.T) {
	// 校验不过 passthrough（无此码 → 优惠码无效）。
	_, db := newTestRouterWithDB(t)
	if _, _, err := applyCouponTx(db, "NOPE", &storage.User{}, &storage.CardBatch{}, 1000, time.Now()); err == nil {
		t.Fatal("passthrough: want err")
	}

	// 条件核销零行（used 已顶 total）报已领完。
	_, db = newTestRouterWithDB(t)
	seedCouponRow(t, db, "CP2", "cut", 10, 1, 1, 0)
	_, _, err := applyCouponTx(db, "CP2", &storage.User{}, &storage.CardBatch{}, 1000, time.Now())
	if err == nil || !containsStr(err.Error(), "已领完") {
		t.Fatalf("burn exhausted: %v", err)
	}

	// 核销成功：cut 减额落实付 990 且 used+1。
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
