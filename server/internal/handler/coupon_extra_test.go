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

// TestCouponClaimExhausted 限次码领完即止：第二单核销被条件更新挡下，
// 400 不落单（total=0 不限的反面口径）。
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
