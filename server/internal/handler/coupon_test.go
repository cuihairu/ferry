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

// promo 批（PROMO-1）测试：优惠码管理面 CRUD 校验 + panel 下单核销
// （金额/账目三列/原子限次/窗口门槛范围/网关失败不烧码）。

func mkCouponBody(kind string, value int64) map[string]any {
	return map[string]any{"kind": kind, "value": value}
}

// couponID 格式化路由里的 id 段。
func couponID(id uint) string { return strconv.FormatUint(uint64(id), 10) }

// TestCouponCRUDValidation 覆盖建/改/删校验：kind 枚举、pct 基点边界、
// cut 正数、负值拒收、scope 形态与批次存在性、时间窗顺序、自动码、
// 重复码 409、per_user 缺省 1。
func TestCouponCRUDValidation(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	batch := storage.CardBatch{Name: "包月", GrantType: "extend_days", GrantValue: 30, PriceCents: 1000, Total: 10, CreatedBy: "admin"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}

	// 合法建码：显式码 + per_user 缺省
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

	// 自动码
	rec = doJSON(t, r, "POST", "/api/coupons", mkCouponBody("cut", 100))
	if rec.Code != http.StatusCreated {
		t.Fatalf("auto code: %d %s", rec.Code, rec.Body)
	}
	var auto storage.Coupon
	json.Unmarshal(rec.Body.Bytes(), &auto)
	if !strings.HasPrefix(auto.Code, "FERRY-") || len(auto.Code) != len("FERRY-")+8 {
		t.Fatalf("auto code = %q", auto.Code)
	}

	// 重复码 409
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"code": "SAVE200", "kind": "cut", "value": 300}); rec.Code != http.StatusConflict {
		t.Fatalf("dup code: %d", rec.Code)
	}

	// kind 枚举与数值边界
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
	// pct 基点上边界内 9999 可收
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"kind": "pct", "value": 9999}); rec.Code != http.StatusCreated {
		t.Fatalf("pct 9999: %d %s", rec.Code, rec.Body)
	}

	// scope：合法批次、不存在批次、坏形态
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"kind": "cut", "value": 100, "scope": "batch:" + couponID(batch.ID)}); rec.Code != http.StatusCreated {
		t.Fatalf("scope batch: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"kind": "cut", "value": 100, "scope": "batch:99999"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing batch scope: %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"kind": "cut", "value": 100, "scope": "node:1"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad scope form: %d", rec.Code)
	}

	// 时间窗：end 早于 start 拒收
	bad := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	good := time.Now().Add(time.Hour).Format(time.RFC3339)
	if rec = doJSON(t, r, "POST", "/api/coupons", map[string]any{"kind": "cut", "value": 100, "starts_at": good, "ends_at": bad}); rec.Code != http.StatusBadRequest {
		t.Fatalf("window inverted: %d", rec.Code)
	}

	// 编辑：整包更新
	rec = doJSON(t, r, "PUT", "/api/coupons/"+couponID(row.ID), map[string]any{"code": "SAVE200", "kind": "pct", "value": 500})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var updated storage.Coupon
	json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Kind != "pct" || updated.Value != 500 {
		t.Fatalf("updated = %+v", updated)
	}
	// 编辑改码撞已有码 409
	if rec = doJSON(t, r, "PUT", "/api/coupons/"+couponID(updated.ID), map[string]any{"code": auto.Code, "kind": "pct", "value": 500}); rec.Code != http.StatusConflict {
		t.Fatalf("update dup code: %d", rec.Code)
	}
	if rec = doJSON(t, r, "PUT", "/api/coupons/99999", mkCouponBody("cut", 100)); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: %d", rec.Code)
	}

	// 列表 + 删除 + 删除后 404
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

// TestCouponApplyOnOrder 覆盖下单核销闭环（PROMO-1）：cut/pct 金额与下限
// 0、账目三列（list/amount/promo 快照）、未知码/未开始/过期/门槛/范围/
// 每用户限次/总量限次逐项 400、网关失败 502 不烧码、无码单三列零值。
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
	// doPanelFrom 以指定来源 IP 发请求：下单限流按 IP 计（10 次/分钟），
	// 本用例单数超限，每次下单轮换 IP 隔离。
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

	// cut：减 200 实付 800；大小写不敏感；账目三列与快照齐
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

	// pct：25% 折后 750
	mk("PCT25", mkCouponBody("pct", 2500))
	if code, out = order("PCT25"); code != http.StatusCreated || out["amount_cents"].(float64) != 750 {
		t.Fatalf("pct order: %d %v", code, out)
	}

	// 减额下限 0
	mk("BIGCUT", mkCouponBody("cut", 5000))
	if code, out = order("BIGCUT"); code != http.StatusCreated || out["amount_cents"].(float64) != 0 {
		t.Fatalf("floor order: %d %v", code, out)
	}

	// 无码单：金额=面价、promo 列零值
	if code, out = order(""); code != http.StatusCreated || out["amount_cents"].(float64) != 1000 {
		t.Fatalf("plain order: %d %v", code, out)
	}
	var plain storage.PaymentOrder
	db.Where("order_no = ?", out["order_no"]).First(&plain)
	if plain.ListAmountCents != 1000 || plain.PromoCode != "" || plain.PromoSnapshot != "" {
		t.Fatalf("plain order = %+v", plain)
	}

	// 未知码：400 不落单
	if code, _ = order("NOSUCH"); code != http.StatusBadRequest || orderCount() != 4 {
		t.Fatalf("unknown coupon: %d count=%d", code, orderCount())
	}

	// 窗口：未开始 / 已过期
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

	// 门槛：min_amount 高于面价
	mk("MIN5000", map[string]any{"kind": "cut", "value": 100, "min_amount": 5000})
	if code, _ = order("MIN5000"); code != http.StatusBadRequest {
		t.Fatalf("min_amount: %d", code)
	}

	// 范围：仅另一批次可用
	mk("ONLYB2", map[string]any{"kind": "cut", "value": 100, "scope": "batch:" + couponID(batches[1].ID)})
	if code, _ = order("ONLYB2"); code != http.StatusBadRequest {
		t.Fatalf("scope mismatch: %d", code)
	}

	// 每用户限次：第二次同码 400
	mk("ONCE", mkCouponBody("cut", 100))
	if code, _ = order("ONCE"); code != http.StatusCreated {
		t.Fatalf("once first: %d", code)
	}
	if code, _ = order("ONCE"); code != http.StatusBadRequest {
		t.Fatalf("once second should 400: %d", code)
	}

	// 总量限次：total=1 第二单 400
	mk("LAST", map[string]any{"kind": "cut", "value": 100, "total": 1})
	if code, _ = order("LAST"); code != http.StatusCreated {
		t.Fatalf("last first: %d", code)
	}
	if code, _ = order("LAST"); code != http.StatusBadRequest {
		t.Fatalf("total reached should 400: %d", code)
	}

	// 网关失败 502：不烧码、不落单（新码验证，避免与已核销单混淆）
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
