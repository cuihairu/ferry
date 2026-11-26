package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestOrderExpirySweep pins the unified order timeout rule (F-1303): the sweep
// expires only pending rows past the 30-minute horizon; fresh pending, paid,
// cancelled and refunded orders are untouched.
func TestOrderExpirySweep(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	now := time.Now()
	old := now.Add(-orderPendingTimeout - time.Minute)
	orders := []storage.PaymentOrder{
		{OrderNo: "exp-old", UserID: 1, Provider: "epusdt", Status: "pending", CreatedAt: old},
		{OrderNo: "exp-fresh", UserID: 1, Provider: "epusdt", Status: "pending", CreatedAt: now},
		{OrderNo: "exp-paid", UserID: 1, Provider: "epusdt", Status: "paid", CreatedAt: old},
		{OrderNo: "exp-gone", UserID: 1, Provider: "epusdt", Status: "cancelled", CreatedAt: old},
		{OrderNo: "exp-refd", UserID: 1, Provider: "epusdt", Status: "refunded", CreatedAt: old},
	}
	if err := db.Create(&orders).Error; err != nil {
		t.Fatal(err)
	}

	n, err := OrderSweep(db, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("sweep touched %d rows, want 1", n)
	}
	for _, want := range []struct{ no, status string }{
		{"exp-old", "expired"}, {"exp-fresh", "pending"}, {"exp-paid", "paid"},
		{"exp-gone", "cancelled"}, {"exp-refd", "refunded"},
	} {
		var o storage.PaymentOrder
		if err := db.Where("order_no = ?", want.no).First(&o).Error; err != nil {
			t.Fatal(err)
		}
		if o.Status != want.status {
			t.Fatalf("%s = %s, want %s", want.no, o.Status, want.status)
		}
	}
}

// TestPanelOrderCancel pins the cancel rule (F-1303): the owner cancels a
// pending order once; re-cancel, a foreign order and a terminal order all
// conflict, and the owner-scoped lookup hides other users' order numbers.
func TestPanelOrderCancel(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	tok := panelUserOf(t, r, "cancel-buyer", nil)
	panelUserOf(t, r, "cancel-other", nil)

	var buyer, foreign storage.User
	if err := db.Where("username = ?", "cancel-buyer").First(&buyer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("username = ?", "cancel-other").First(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	orders := []storage.PaymentOrder{
		{OrderNo: "cxl-mine", UserID: buyer.ID, Provider: "epusdt", Status: "pending", AmountCents: 1000, CreatedAt: time.Now()},
		{OrderNo: "cxl-theirs", UserID: foreign.ID, Provider: "epusdt", Status: "pending", AmountCents: 1000, CreatedAt: time.Now()},
		{OrderNo: "cxl-paid", UserID: buyer.ID, Provider: "epusdt", Status: "paid", AmountCents: 1000, CreatedAt: time.Now()},
	}
	if err := db.Create(&orders).Error; err != nil {
		t.Fatal(err)
	}

	if rec := doPanel(t, r, tok, "POST", "/api/panel/orders/cxl-mine/cancel", nil); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	var o storage.PaymentOrder
	if err := db.Where("order_no = ?", "cxl-mine").First(&o).Error; err != nil {
		t.Fatal(err)
	}
	if o.Status != "cancelled" {
		t.Fatalf("status = %s, want cancelled", o.Status)
	}

	// Re-cancel: the order is no longer pending.
	if rec := doPanel(t, r, tok, "POST", "/api/panel/orders/cxl-mine/cancel", nil); rec.Code != http.StatusConflict {
		t.Fatalf("re-cancel: %d", rec.Code)
	}
	// A foreign order_no resolves to nothing through the owner-scoped lookup.
	if rec := doPanel(t, r, tok, "POST", "/api/panel/orders/cxl-theirs/cancel", nil); rec.Code != http.StatusConflict {
		t.Fatalf("foreign cancel: %d", rec.Code)
	}
	// Terminal states never cancel.
	if rec := doPanel(t, r, tok, "POST", "/api/panel/orders/cxl-paid/cancel", nil); rec.Code != http.StatusConflict {
		t.Fatalf("paid cancel: %d", rec.Code)
	}
	var theirs storage.PaymentOrder
	if err := db.Where("order_no = ?", "cxl-theirs").First(&theirs).Error; err != nil {
		t.Fatal(err)
	}
	if theirs.Status != "pending" {
		t.Fatalf("foreign order mutated: %s", theirs.Status)
	}
}

// TestSettleExpiredOrderStaysExpired pins the settle-side half of the rule
// (F-1303): a callback arriving after expiry never revives the order and never
// grants -- the transaction row still lands (the money really did arrive) so
// payrecon surfaces the leg for review.
func TestSettleExpiredOrderStaysExpired(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	panelUserOf(t, r, "late-buyer", nil)
	var u storage.User
	if err := db.Where("username = ?", "late-buyer").First(&u).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	order := storage.PaymentOrder{
		OrderNo: "late-cb", UserID: u.ID, Provider: "epusdt", Status: "expired",
		AmountCents: 1000, GrantType: "add_quota", GrantValue: 50 << 30,
		CreatedAt: now.Add(-orderPendingTimeout - time.Minute),
	}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}

	h := &Handler{db: db}
	out, err := h.settlePayment("epusdt", payment.Callback{
		OrderNo: "late-cb", ExternalID: "txid-late", AmountCents: 1000,
		PaidAt: now, Raw: []byte(`{"late":true}`),
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if out["idempotent"] != true {
		t.Fatalf("settle out = %+v, want the idempotent short-circuit", out)
	}
	var fresh storage.PaymentOrder
	if err := db.Where("order_no = ?", "late-cb").First(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.Status != "expired" {
		t.Fatalf("revived to %s, want expired", fresh.Status)
	}
	// The money row still lands (payrecon will flag the missing leg).
	var txn int64
	db.Model(&storage.PaymentTransaction{}).Where("external_id = ?", "txid-late").Count(&txn)
	if txn != 1 {
		t.Fatalf("transaction rows = %d, want 1", txn)
	}
	// No grant: the user's quota is untouched.
	var after storage.User
	if err := db.First(&after, u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.QuotaBytes != u.QuotaBytes {
		t.Fatalf("quota changed %d -> %d on an expired order", u.QuotaBytes, after.QuotaBytes)
	}
}
