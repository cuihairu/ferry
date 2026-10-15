package handler

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// seedPayOrder 落付款用户与待支付订单，返回与订单匹配的回调。
func seedPayOrder(t *testing.T, db *gorm.DB, orderNo string) payment.Callback {
	t.Helper()
	u := storage.User{Username: "p-" + orderNo, SubToken: "sub-" + orderNo,
		QuotaBytes: 1 << 30, Enabled: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: orderNo, UserID: u.ID, Provider: "epusdt", AmountCents: 1000,
		Product: "50GB 流量包", Status: "pending",
		GrantType: "add_quota", GrantValue: 5 << 30,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return payment.Callback{
		OrderNo: orderNo, ExternalID: "tx-" + orderNo, AmountCents: 1000,
		PaidAt: time.Now(), Raw: []byte("amount=10.00&order_id=" + orderNo),
	}
}

// TestEpusdtNotifyDBErrors 覆盖回调结算事务内的取数与落库失败：对单
// 定位、重复回调计数、流水建档、订单置 paid、付款用户定位、权益发放
// 各自注错统一 400，事务整体回滚订单保持 pending。
// 留白：渠道未注册 503——payment 注册表无撤销入口，跨用例与 -count=2
// 重跑间状态不定，不硬凑。
func TestEpusdtNotifyDBErrors(t *testing.T) {
	scenario := func(name string, inject func(db *gorm.DB)) {
		t.Run(name, func(t *testing.T) {
			r, db := newTestRouterWithDB(t)
			cb := seedPayOrder(t, db, "pn-"+name)
			inject(db)
			payment.Register(stubEpusdt{cb: cb})
			if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
			}
			// 回读走 Raw+Scan（Row 链不吃 Query 注错），事务应回滚。
			var status string
			if err := db.Raw("SELECT status FROM payment_orders WHERE order_no = ?", cb.OrderNo).
				Scan(&status).Error; err != nil {
				t.Fatal(err)
			}
			if status != "pending" {
				t.Fatalf("%s: order leaked to %q", name, status)
			}
		})
	}
	scenario("order find", func(db *gorm.DB) {
		regQueryFail("t:pnfind", db, (*storage.PaymentOrder)(nil))
	})
	scenario("dup count", func(db *gorm.DB) {
		failNthCount("t:pndup", db, 1)
	})
	scenario("txn create", func(db *gorm.DB) {
		db.Callback().Create().Before("gorm:create").Register("t:pncreate", func(tx *gorm.DB) {
			if _, ok := tx.Statement.Dest.(*storage.PaymentTransaction); ok {
				tx.AddError(errors.New("db down"))
			}
		})
	})
	scenario("order paid", func(db *gorm.DB) {
		db.Callback().Update().Before("gorm:update").Register("t:pnpaid", func(tx *gorm.DB) {
			if _, ok := tx.Statement.Model.(*storage.PaymentOrder); ok {
				tx.AddError(errors.New("db down"))
			}
		})
	})
	scenario("user find", func(db *gorm.DB) {
		regQueryFail("t:pnuser", db, (*storage.User)(nil))
	})
	scenario("grant emit", func(db *gorm.DB) {
		db.Callback().Create().Before("gorm:create").Register("t:pngrant", func(tx *gorm.DB) {
			if _, ok := tx.Statement.Dest.(*storage.Grant); ok {
				tx.AddError(errors.New("db down"))
			}
		})
	})
}

// TestEpusdtNotifyNoGrant 覆盖到账无发放口径（GrantType 空）分支：结算
// 成功置 paid 但不发放、不落 Grant 行。
func TestEpusdtNotifyNoGrant(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	cb := seedPayOrder(t, db, "pn-nogrant")
	if err := db.Model(&storage.PaymentOrder{}).Where("order_no = ?", cb.OrderNo).
		Update("grant_type", "").Error; err != nil {
		t.Fatal(err)
	}
	payment.Register(stubEpusdt{cb: cb})
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusOK {
		t.Fatalf("no-grant settle: %d %s", rec.Code, rec.Body)
	}
	var grants, txns int64
	db.Model(&storage.Grant{}).Count(&grants)
	db.Model(&storage.PaymentTransaction{}).Count(&txns)
	if grants != 0 || txns != 1 {
		t.Fatalf("rows = grants %d txns %d, want 0/1", grants, txns)
	}
	var status string
	db.Raw("SELECT status FROM payment_orders WHERE order_no = ?", cb.OrderNo).Scan(&status)
	if status != "paid" {
		t.Fatalf("order = %s, want paid", status)
	}
}
