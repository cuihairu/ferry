package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/packages/payment"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCouponClaimExhausted a capped code stops once used up: the second order's redemption is
// blocked by the conditional update, and 400 leaves no order behind (the converse of the total=0 unlimited contract).
func TestCouponClaimExhausted(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if err := db.Create(&storage.User{
		Username: "buyer", SubToken: "buyer-sub", QuotaBytes: 1 << 30, Enabled: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	batch := storage.CardBatch{Name: "限量包", GrantType: "add_quota", GrantValue: 10 << 30,
		PriceCents: 1000, Total: 100, CreatedBy: "admin"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.CardCode{BatchID: batch.ID, Code: "LIMIT1-CODE", Status: "unused"}).Error; err != nil {
		t.Fatal(err)
	}
	payment.Register(stubGateway{})

	body := mkCouponBody("cut", 100)
	body["code"] = "LIMIT1"
	body["total"] = 1
	if rec := doJSON(t, r, "POST", "/api/coupons", body); rec.Code != http.StatusCreated {
		t.Fatalf("mk coupon: %d %s", rec.Code, rec.Body)
	}

	order := func(ip string) (int, string) {
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(map[string]any{"batch_id": batch.ID, "provider": "epusdt", "coupon_code": "LIMIT1"})
		req := httptest.NewRequest("POST", "/api/panel/orders", &buf)
		req.RemoteAddr = ip + ":1234"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer buyer-sub")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	code, out := order("10.8.0.1")
	if code != http.StatusCreated {
		t.Fatalf("first order: %d %s", code, out)
	}
	code, out = order("10.8.0.2")
	if code != http.StatusBadRequest || !strings.Contains(out, "优惠码已领完") {
		t.Fatalf("second order: %d %s; want exhausted 400", code, out)
	}
	var cp storage.Coupon
	if err := db.Where("code = ?", "LIMIT1").First(&cp).Error; err != nil {
		t.Fatal(err)
	}
	if cp.Used != 1 {
		t.Fatalf("used = %d; want exactly 1", cp.Used)
	}
	var n int64
	db.Model(&storage.PaymentOrder{}).Count(&n)
	if n != 1 {
		t.Fatalf("orders = %d; want 1 (failed one rolled back)", n)
	}
}

// TestCouponPendingDoesNotOccupyPerUserLimit covers the P2 "coupon pending occupies the cap"
// fix: the per-user cap only counts settled/refunded orders. Before the fix, an order was set to pending at creation and this
// repo has no order expiry sweeper, so a single unpaid order permanently consumed the user's per-user cap (a second use of
// the code → 400 per-user once only). After the fix pending is not counted — repeat coded orders from the same user are allowed;
// once an order settles (paid) it counts toward the cap as usual, and the next one → 400.
func TestCouponPendingDoesNotOccupyPerUserLimit(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if err := db.Create(&storage.User{
		Username: "buyer", SubToken: "buyer-sub", QuotaBytes: 1 << 30, Enabled: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	batch := storage.CardBatch{Name: "限次包", GrantType: "add_quota", GrantValue: 10 << 30,
		PriceCents: 1000, Total: 100, CreatedBy: "admin"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.CardCode{BatchID: batch.ID, Code: "PEND1-CODE", Status: "unused"}).Error; err != nil {
		t.Fatal(err)
	}
	payment.Register(stubGateway{})

	body := mkCouponBody("cut", 100)
	body["code"] = "PEND1"
	body["per_user"] = 1
	if rec := doJSON(t, r, "POST", "/api/coupons", body); rec.Code != http.StatusCreated {
		t.Fatalf("mk coupon: %d %s", rec.Code, rec.Body)
	}

	order := func(ip string) (int, string) {
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(map[string]any{"batch_id": batch.ID, "provider": "epusdt", "coupon_code": "PEND1"})
		req := httptest.NewRequest("POST", "/api/panel/orders", &buf)
		req.RemoteAddr = ip + ":1234"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer buyer-sub")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	// The first order lands as pending; the second with the same code should pass (pending does not occupy the per-user cap).
	code, out := order("10.9.1.1")
	if code != http.StatusCreated {
		t.Fatalf("first order: %d %s", code, out)
	}
	if code, out = order("10.9.1.2"); code != http.StatusCreated {
		t.Fatalf("second order should pass (pending not counted): %d %s", code, out)
	}

	// After the first order settles to paid, paid counts toward the cap as usual: the third order → 400.
	var first storage.PaymentOrder
	if err := db.Where("promo_code = ?", "PEND1").Order("id").First(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.PaymentOrder{}).Where("id = ?", first.ID).Update("status", "paid").Error; err != nil {
		t.Fatal(err)
	}
	if code, out = order("10.9.1.3"); code != http.StatusBadRequest || !strings.Contains(out, "每用户限用一次") {
		t.Fatalf("third order after paid: %d %s; want per-user limit 400", code, out)
	}
}
