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

// seedPayOrder creates the paying user and a pending order, returning the callback that matches the order.
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

// TestEpusdtNotifyDBErrors covers read and write failures inside the
// callback settle transaction: order lookup, duplicate-callback count,
// transaction insert, marking the order paid, payer lookup, and grant
// emission. Each injected failure yields a uniform 400, the whole
// transaction rolls back, and the order stays pending.
// Deliberately not covered: unregistered channel 503 -- the payment
// registry has no unregister hook, and state drifts across cases and
// -count=2 reruns, so it is not worth forcing.
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
			// Read back via Raw+Scan (the Row chain does not honor Query
			// injection); the transaction must have rolled back.
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

// TestEpusdtNotifyNoGrant covers the no-grant-spec branch on receipt
// (GrantType empty): the settle succeeds and marks paid, but grants
// nothing and writes no Grant row.
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
