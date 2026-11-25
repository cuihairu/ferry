package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// promo batch (PROMO-1) tests: coupon admin CRUD validation + panel order redemption
// (amount / three accounting columns / atomic cap / window threshold scope / gateway failure does not burn the code).

func mkCouponBody(kind string, value int64) map[string]any {
	return map[string]any{"kind": kind, "value": value}
}

// couponID formats the id segment of the route.
func couponID(id uint) string { return strconv.FormatUint(uint64(id), 10) }

// TestCouponCRUDValidation covers create/edit/delete validation: kind enum, pct basis-point bounds,
// positive cut, negative-value rejection, scope shape and batch existence, time-window order, auto code,
// duplicate code 409, per_user default 1.
func TestCouponCRUDValidation(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	batch := storage.CardBatch{Name: "包月", GrantType: "extend_days", GrantValue: 30, PriceCents: 1000, Total: 10, CreatedBy: "admin"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}

	// Valid create: explicit code + default per_user
	rec := doJSON(t, r, "POST", "/api/coupons", map[string]any{"code": "save200", "kind": "cut", "value": 200})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var row storage.Coupon
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.Code != "SAVE200" || row.PerUser != 1 || row.Used != 0 || row.Scope != "all" {
		t.Fatalf("coupon = %+v (code 应大写、per_user 缺省 1)", row)
	}

	// Auto-generated code
	rec = doJSON(t, r, "POST", "/api/coupons", mkCouponBody("cut", 100))
	if rec.Code != http.StatusCreated {
		t.Fatalf("auto code: %d %s", rec.Code, rec.Body)
	}
	var auto storage.Coupon
	json.Unmarshal(rec.Body.Bytes(), &auto)
	if !strings.HasPrefix(auto.Code, "FERRY-") || len(auto.Code) != len("FERRY-")+8 {
		t.Fatalf("auto code = %q", auto.Code)
	}

	// Duplicate code 409
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"code": "SAVE200", "kind": "cut", "value": 300}); rec.Code != http.StatusConflict {
		t.Fatalf("dup code: %d", rec.Code)
	}

	// kind enum and numeric bounds
	for name, body := range map[string]map[string]any{
		"bad kind":    {"kind": "half", "value": 100},
		"pct zero":    {"kind": "pct", "value": 0},
		"pct 10000":   {"kind": "pct", "value": 10000},
		"cut zero":    {"kind": "cut", "value": 0},
		"cut neg":     {"kind": "cut", "value": -5},
		"neg total":   {"kind": "cut", "value": 100, "total": -1},
		"neg peruser": {"kind": "cut", "value": 100, "per_user": -1},
	} {
		if rec = doJSON(t, r, "POST", "/api/coupons", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s should 400: %d %s", name, rec.Code, rec.Body)
		}
	}
	// pct basis point 9999, inside the upper bound, is accepted
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"kind": "pct", "value": 9999}); rec.Code != http.StatusCreated {
		t.Fatalf("pct 9999: %d %s", rec.Code, rec.Body)
	}

	// scope: valid batch, nonexistent batch, bad shape
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"kind": "cut", "value": 100, "scope": "batch:" + couponID(batch.ID)}); rec.Code != http.StatusCreated {
		t.Fatalf("scope batch: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"kind": "cut", "value": 100, "scope": "batch:99999"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing batch scope: %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"kind": "cut", "value": 100, "scope": "node:1"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad scope form: %d", rec.Code)
	}

	// Time window: end earlier than start is rejected
	bad := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	good := time.Now().Add(time.Hour).Format(time.RFC3339)
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"kind": "cut", "value": 100, "starts_at": good, "ends_at": bad}); rec.Code != http.StatusBadRequest {
		t.Fatalf("window inverted: %d", rec.Code)
	}

	// Edit: full update
	rec = doJSON(t, r, "PUT", "/api/coupons/"+couponID(row.ID), map[string]any{"code": "SAVE200", "kind": "pct", "value": 500})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var updated storage.Coupon
	json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Kind != "pct" || updated.Value != 500 {
		t.Fatalf("updated = %+v", updated)
	}
	// Edit that collides with an existing code → 409
	if rec = doJSON(t, r, "PUT", "/api/coupons/"+couponID(updated.ID), map[string]any{"code": auto.Code, "kind": "pct", "value": 500}); rec.Code != http.StatusConflict {
		t.Fatalf("update dup code: %d", rec.Code)
	}
	if rec = doJSON(t, r, "PUT", "/api/coupons/99999", mkCouponBody("cut", 100)); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: %d", rec.Code)
	}

	// List + delete + 404 after delete
	if rec = doJSON(t, r, "GET", "/api/coupons", nil); rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	if rec = doJSON(t, r, "DELETE", "/api/coupons/"+couponID(updated.ID), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec = doJSON(t, r, "DELETE", "/api/coupons/"+couponID(updated.ID), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}
}

// TestCouponApplyOnOrder covers the order-redemption loop (PROMO-1): cut/pct amount and the floor of 0,
// three accounting columns (list/amount/promo snapshot), unknown code / not-started / expired / threshold /
// scope / per-user cap / total cap each a 400 per case, gateway failure 502 without burning the code, and a
// no-code order with zeroed columns.
func TestCouponApplyOnOrder(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	for _, name := range []string{"buyer"} {
		if err := db.Create(&storage.User{
			Username: name, SubToken: name + "-sub", QuotaBytes: 1 << 30, Enabled: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	batches := []storage.CardBatch{
		{Name: "百元包", GrantType: "add_quota", GrantValue: 50 << 30, PriceCents: 1000, Total: 100, CreatedBy: "admin"},
		{Name: "另品", GrantType: "extend_days", GrantValue: 30, PriceCents: 2000, Total: 10, CreatedBy: "admin"},
	}
	for i := range batches {
		if err := db.Create(&batches[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []storage.CardCode{
		{BatchID: batches[0].ID, Code: "CP-A1", Status: "unused"},
		{BatchID: batches[1].ID, Code: "CP-B1", Status: "unused"},
	} {
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	sell := batches[0]
	payment.Register(stubGateway{})
	// doPanelFrom sends the request from a specified source IP: order rate limiting is counted per IP (10 per minute),
	// and this case's order count exceeds it, so each order rotates to a different IP for isolation.
	var orderSeq int
	doPanelFrom := func(ip, coupon string) *httptest.ResponseRecorder {
		body := map[string]any{"batch_id": sell.ID, "provider": "epusdt"}
		if coupon != "" {
			body["coupon_code"] = coupon
		}
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/api/panel/orders", &buf)
		req.RemoteAddr = ip + ":1234"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer buyer-sub")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	order := func(coupon string) (int, map[string]any) {
		orderSeq++
		rec := doPanelFrom(fmt.Sprintf("10.1.0.%d", orderSeq), coupon)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	couponRow := func(code string) storage.Coupon {
		var cp storage.Coupon
		if err := db.Where("code = ?", code).First(&cp).Error; err != nil {
			t.Fatal(err)
		}
		return cp
	}
	mk := func(code string, body map[string]any) storage.Coupon {
		body["code"] = code
		rec := doJSON(t, r, "POST", "/api/coupons", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("mk %s: %d %s", code, rec.Code, rec.Body)
		}
		var cp storage.Coupon
		json.Unmarshal(rec.Body.Bytes(), &cp)
		return cp
	}
	orderCount := func() int64 {
		var n int64
		db.Model(&storage.PaymentOrder{}).Count(&n)
		return n
	}

	// cut: minus 200 → pays 800; case-insensitive; the three accounting columns and snapshot all present
	mk("CUT200", mkCouponBody("cut", 200))
	code, out := order("cut200")
	if code != http.StatusCreated || out["amount_cents"].(float64) != 800 {
		t.Fatalf("cut order: %d %v", code, out)
	}
	var ord storage.PaymentOrder
	db.Where("order_no = ?", out["order_no"]).First(&ord)
	if ord.AmountCents != 800 || ord.ListAmountCents != 1000 || ord.PromoCode != "CUT200" {
		t.Fatalf("order = %+v", ord)
	}
	if !strings.Contains(ord.PromoSnapshot, `"discount_cents":200`) || !strings.Contains(ord.PromoSnapshot, `"code":"CUT200"`) {
		t.Fatalf("snapshot = %s", ord.PromoSnapshot)
	}
	if got := couponRow("CUT200").Used; got != 1 {
		t.Fatalf("used = %d, want 1", got)
	}

	// pct: 25% off → 750
	mk("PCT25", mkCouponBody("pct", 2500))
	if code, out = order("PCT25"); code != http.StatusCreated || out["amount_cents"].(float64) != 750 {
		t.Fatalf("pct order: %d %v", code, out)
	}

	// Floor of 0 for the discount
	mk("BIGCUT", mkCouponBody("cut", 5000))
	if code, out = order("BIGCUT"); code != http.StatusCreated || out["amount_cents"].(float64) != 0 {
		t.Fatalf("floor order: %d %v", code, out)
	}

	// No-code order: amount = list price, promo columns zeroed
	if code, out = order(""); code != http.StatusCreated || out["amount_cents"].(float64) != 1000 {
		t.Fatalf("plain order: %d %v", code, out)
	}
	var plain storage.PaymentOrder
	db.Where("order_no = ?", out["order_no"]).First(&plain)
	if plain.ListAmountCents != 1000 || plain.PromoCode != "" || plain.PromoSnapshot != "" {
		t.Fatalf("plain order = %+v", plain)
	}

	// Unknown code: 400, no order landed
	if code, _ = order("NOSUCH"); code != http.StatusBadRequest || orderCount() != 4 {
		t.Fatalf("unknown coupon: %d count=%d", code, orderCount())
	}

	// Window: not started / expired
	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	mk("FUTURE", map[string]any{"kind": "cut", "value": 100, "starts_at": future})
	mk("PAST", map[string]any{"kind": "cut", "value": 100, "ends_at": past})
	if code, _ = order("FUTURE"); code != http.StatusBadRequest {
		t.Fatalf("not started: %d", code)
	}
	if code, _ = order("PAST"); code != http.StatusBadRequest {
		t.Fatalf("expired: %d", code)
	}

	// Threshold: min_amount above the list price
	mk("MIN5000", map[string]any{"kind": "cut", "value": 100, "min_amount": 5000})
	if code, _ = order("MIN5000"); code != http.StatusBadRequest {
		t.Fatalf("min_amount: %d", code)
	}

	// Scope: only valid for the other batch
	mk("ONLYB2", map[string]any{"kind": "cut", "value": 100, "scope": "batch:" + couponID(batches[1].ID)})
	if code, _ = order("ONLYB2"); code != http.StatusBadRequest {
		t.Fatalf("scope mismatch: %d", code)
	}

	// Per-user cap: after the first order settles to paid, a second use of the same code → 400 (pending does not occupy the cap —
	// this repo has no order expiry sweeper, and since the P2 fix only settled orders count, see
	// TestCouponPendingDoesNotOccupyPerUserLimit)
	mk("ONCE", mkCouponBody("cut", 100))
	if code, out = order("ONCE"); code != http.StatusCreated {
		t.Fatalf("once first: %d", code)
	}
	db.Model(&storage.PaymentOrder{}).Where("order_no = ?", out["order_no"]).Update("status", "paid")
	if code, _ = order("ONCE"); code != http.StatusBadRequest {
		t.Fatalf("once second after paid should 400: %d", code)
	}

	// Total cap: total=1, the second order → 400
	mk("LAST", map[string]any{"kind": "cut", "value": 100, "total": 1})
	if code, _ = order("LAST"); code != http.StatusCreated {
		t.Fatalf("last first: %d", code)
	}
	if code, _ = order("LAST"); code != http.StatusBadRequest {
		t.Fatalf("total reached should 400: %d", code)
	}

	// Gateway failure 502: does not burn the code, does not land an order (fresh code to keep it distinct from redeemed ones)
	mk("NOBURN", mkCouponBody("cut", 100))
	payment.Register(stubEpusdt{err: errors.New("网关不可用")})
	if code, _ = order("NOBURN"); code != http.StatusBadGateway {
		t.Fatalf("gateway failure: %d", code)
	}
	if got := couponRow("NOBURN").Used; got != 0 {
		t.Fatalf("coupon burned on gateway failure: used=%d", got)
	}
	if orderCount() != 6 {
		t.Fatalf("gateway failure landed order: count=%d", orderCount())
	}
}
