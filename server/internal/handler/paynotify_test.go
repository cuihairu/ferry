package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// stubEpusdt is a controllable fake epusdt provider: Verify returns a preset verdict.
type stubEpusdt struct {
	cb  payment.Callback
	err error
}

func (s stubEpusdt) Name() string { return "epusdt" }
func (s stubEpusdt) CreateOrder(context.Context, payment.Order) (payment.Receipt, error) {
	return payment.Receipt{}, errors.New("stub 未实现下单")
}
func (s stubEpusdt) Verify(context.Context, []byte) (payment.Callback, error) {
	return s.cb, s.err
}

// TestEpusdtNotify covers callback settlement (PAY-8): failed signature
// check 400, order validation (missing / amount mismatch / channel
// mismatch), a successful three-ledger settle plus grant, and idempotent
// replay of a duplicate callback.
func TestEpusdtNotify(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if err := db.Create(&storage.User{
		Username: "payer", SubToken: "payer-sub", QuotaBytes: 1 << 30, Enabled: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: "epusdt-o-1", UserID: 1, Provider: "epusdt", AmountCents: 1000,
		Product: "50GB 流量包", Status: "pending",
		GrantType: "add_quota", GrantValue: 5 << 30,
	}).Error; err != nil {
		t.Fatal(err)
	}

	paidAt := time.Now()
	okCb := payment.Callback{
		OrderNo: "epusdt-o-1", ExternalID: "tx-9", AmountCents: 1000,
		PaidAt: paidAt, Raw: []byte("amount=10.00&order_id=epusdt-o-1&status=2"),
	}

	// Failed signature check -> 400, nothing written to the ledger
	payment.Register(stubEpusdt{err: errors.New("签名不符")})
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad signature should 400: %d %s", rec.Code, rec.Body)
	}

	// Order not found -> 400
	payment.Register(stubEpusdt{cb: payment.Callback{OrderNo: "no-such", ExternalID: "tx-x", AmountCents: 1000}})
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing order should 400: %d %s", rec.Code, rec.Body)
	}

	// Amount mismatch -> 400, order stays pending
	misCb := okCb
	misCb.AmountCents = 1
	payment.Register(stubEpusdt{cb: misCb})
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("amount mismatch should 400: %d %s", rec.Code, rec.Body)
	}

	// Channel mismatch -> 400
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: "card-o-2", UserID: 1, Provider: "card", Status: "pending",
	}).Error; err != nil {
		t.Fatal(err)
	}
	cardCb := okCb
	cardCb.OrderNo = "card-o-2"
	payment.Register(stubEpusdt{cb: cardCb})
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("provider mismatch should 400: %d %s", rec.Code, rec.Body)
	}

	// Successful settle: transaction row + order paid + grant. The response
	// body is the literal ok (the gateway judges by body, and would treat
	// JSON as a failure and retry forever).
	payment.Register(stubEpusdt{cb: okCb})
	rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("settle: %d %q", rec.Code, rec.Body)
	}
	var txnCount, grantCount int64
	db.Model(&storage.PaymentTransaction{}).Count(&txnCount)
	db.Model(&storage.Grant{}).Count(&grantCount)
	if txnCount != 1 || grantCount != 1 {
		t.Fatalf("rows = txns %d grants %d, want 1/1", txnCount, grantCount)
	}
	var order storage.PaymentOrder
	if err := db.Where("order_no = ?", "epusdt-o-1").First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != "paid" || order.PaidAt == nil {
		t.Fatalf("order = %+v, want paid", order)
	}
	var u storage.User
	db.First(&u, 1)
	if u.QuotaBytes != (1<<30)+(5<<30) {
		t.Fatalf("quota = %d, want +5GB", u.QuotaBytes)
	}
	var grant storage.Grant
	db.First(&grant)
	var snap map[string]any
	if err := json.Unmarshal([]byte(grant.Snapshot), &snap); err != nil {
		t.Fatal(err)
	}
	if snap["trade_id"] != "tx-9" {
		t.Fatalf("snapshot missing trade_id: %v", snap)
	}

	// Duplicate callback (same trade_id) -> 200 with the literal ok body
	// (which stops the gateway from retrying), with no duplicate ledger rows
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("replay should 200 ok: %d %q", rec.Code, rec.Body)
	}
	db.Model(&storage.PaymentTransaction{}).Count(&txnCount)
	db.Model(&storage.Grant{}).Count(&grantCount)
	if txnCount != 1 || grantCount != 1 {
		t.Fatalf("replay duplicated rows: txns %d grants %d", txnCount, grantCount)
	}
	var u2 storage.User
	db.First(&u2, 1)
	if u2.QuotaBytes != u.QuotaBytes {
		t.Fatalf("replay re-granted: %d → %d", u.QuotaBytes, u2.QuotaBytes)
	}
}

// TestEpusdtNotifyConcurrentSettle covers the concurrent settle race
// (P1-3): two callbacks with different external_ids can both clear the
// idempotency check (the order still reads pending on first load); the
// conditional update ensures only one marks it paid and grants. The loser
// keeps its transaction row (the money really did arrive, per the
// reconciliation rule) but gets the idempotent result, so quota is not
// granted twice.
func TestEpusdtNotifyConcurrentSettle(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if err := db.Create(&storage.User{
		Username: "racer", QuotaBytes: 1 << 30, Enabled: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: "race-o-1", UserID: 1, Provider: "epusdt", AmountCents: 1000,
		Status: "pending", GrantType: "add_quota", GrantValue: 2 << 30,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// Simulate queue-jumping: before the idempotency Count runs, another
	// callback has already marked the order paid (the order's first read
	// happens earlier and still sees pending). The injection hangs on the
	// Count (Dest *int64), so the first read is not intercepted.
	db.Callback().Query().Before("gorm:query").Register("t:payrace", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.PaymentTransaction); ok {
			if _, ok := tx.Statement.Dest.(*int64); ok {
				tx.Exec(`UPDATE payment_orders SET status='paid', paid_at=? WHERE order_no = 'race-o-1'`, time.Now())
			}
		}
	})
	payment.Register(stubEpusdt{cb: payment.Callback{
		OrderNo: "race-o-1", ExternalID: "tx-race-2", AmountCents: 1000,
	}})
	rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("lose race: %d %q", rec.Code, rec.Body)
	}
	// The transaction row is kept, but no grant happens.
	var txnCount, grantCount int64
	db.Model(&storage.PaymentTransaction{}).Count(&txnCount)
	db.Model(&storage.Grant{}).Count(&grantCount)
	if txnCount != 1 || grantCount != 0 {
		t.Fatalf("rows = txns %d grants %d, want 1/0", txnCount, grantCount)
	}
	var u storage.User
	db.First(&u, 1)
	if u.QuotaBytes != 1<<30 {
		t.Fatalf("quota = %d, want unchanged", u.QuotaBytes)
	}
}
