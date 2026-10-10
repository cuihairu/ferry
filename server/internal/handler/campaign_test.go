package handler

import (
	"bytes"
	"encoding/json"
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

// 限时活动（PROMO-2）测试：管理面 CRUD 校验 + 活动位（进行中/即将开始）+
// 下单自动适用与取优（码 vs 活动择惠大者、落选码不核销；首单/续费按历史
// 已付订单判定）。

// mkCampaign 建活动载荷。
func mkCampaign(name, kind string, rules any, starts, ends string) map[string]any {
	return map[string]any{
		"name": name, "kind": kind, "rules": rules,
		"starts_at": starts, "ends_at": ends,
	}
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestCampaignCRUDValidation 覆盖建/删校验：kind 枚举、rules JSON 口径、
// 起止必填与顺序。
func TestCampaignCRUDValidation(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	now := time.Now()
	start := now.Add(-time.Hour).Format(time.RFC3339)
	end := now.Add(time.Hour).Format(time.RFC3339)

	rec := doJSON(t, r, "POST", "/api/campaigns",
		mkCampaign("开年折扣", "timed", map[string]any{"kind": "cut", "value": 200}, start, end))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var row storage.Campaign
	json.Unmarshal(rec.Body.Bytes(), &row)

	// pct 规则（基点）
	if rec = doJSON(t, r, "POST", "/api/campaigns",
		mkCampaign("会员折扣", "timed", map[string]any{"kind": "pct", "value": 2500}, start, end)); rec.Code != http.StatusCreated {
		t.Fatalf("pct rules: %d %s", rec.Code, rec.Body)
	}

	for name, body := range map[string]map[string]any{
		"bad kind":          mkCampaign("x", "unknown", map[string]any{"kind": "cut", "value": 100}, start, end),
		"no name":           mkCampaign("", "timed", map[string]any{"kind": "cut", "value": 100}, start, end),
		"bad rules":         mkCampaign("x", "timed", "not-json", start, end),
		"bad kind in rules": mkCampaign("x", "timed", map[string]any{"kind": "half", "value": 100}, start, end),
		"no starts":         {"name": "x", "kind": "timed", "rules": jsonStr(map[string]any{"kind": "cut", "value": 100}), "ends_at": end},
		"no ends":           {"name": "x", "kind": "timed", "rules": jsonStr(map[string]any{"kind": "cut", "value": 100}), "starts_at": start},
		"end before start":  mkCampaign("x", "timed", map[string]any{"kind": "cut", "value": 100}, end, start),
	} {
		if rec = doJSON(t, r, "POST", "/api/campaigns", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s should 400: %d %s", name, rec.Code, rec.Body)
		}
	}

	// 编辑（含停用）与删除
	if rec = doJSON(t, r, "PUT", "/api/campaigns/"+strconv.Itoa(int(row.ID)),
		map[string]any{"name": "开年折扣改", "kind": "renew", "rules": jsonStr(map[string]any{"kind": "cut", "value": 300}),
			"starts_at": start, "ends_at": end, "enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var updated storage.Campaign
	json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Enabled {
		t.Fatalf("enabled = false expected")
	}
	if rec = doJSON(t, r, "PUT", "/api/campaigns/99999", mkCampaign("x", "timed", map[string]any{"kind": "cut", "value": 1}, start, end)); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: %d", rec.Code)
	}
	if rec = doJSON(t, r, "DELETE", "/api/campaigns/"+strconv.Itoa(int(row.ID)), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec = doJSON(t, r, "DELETE", "/api/campaigns/"+strconv.Itoa(int(row.ID)), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}
}

// TestCampaignAutoApply 覆盖活动自动适用与取优（PROMO-2）：首单/续费判定、
// 活动位过滤、码 vs 活动择惠大者、落选码不核销、范围/门槛/窗口失效后回落面价。
func TestCampaignAutoApply(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	for _, name := range []string{"buyer", "old"} {
		if err := db.Create(&storage.User{
			Username: name, SubToken: name + "-sub", QuotaBytes: 1 << 30, Enabled: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var old storage.User
	db.Where("sub_token = ?", "old-sub").First(&old)
	// old 用户已有已付订单（首单资格用尽、续费资格命中）
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: "old-paid-1", UserID: old.ID, Provider: "epusdt",
		AmountCents: 1000, ListAmountCents: 1000, Product: "包月", Status: "paid",
	}).Error; err != nil {
		t.Fatal(err)
	}
	batch := storage.CardBatch{Name: "百元包", GrantType: "add_quota", GrantValue: 50 << 30, PriceCents: 1000, Total: 100, CreatedBy: "admin"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.CardCode{BatchID: batch.ID, Code: "CP-C1", Status: "unused"}).Error; err != nil {
		t.Fatal(err)
	}
	payment.Register(stubGateway{})
	now := time.Now()
	day := now.Format(time.RFC3339)
	endDay := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)

	// 活动：新客首单减 300；续费减 200；限时节减 500（惠最大）；另品专属码活动（范围外）
	newCustomer := doJSON(t, r, "POST", "/api/campaigns", mkCampaign("新客首单", "first_order",
		map[string]any{"kind": "cut", "value": 300}, day, endDay))
	if newCustomer.Code != http.StatusCreated {
		t.Fatalf("mk first_order: %d", newCustomer.Code)
	}
	renewal := doJSON(t, r, "POST", "/api/campaigns", mkCampaign("老客续费", "renew",
		map[string]any{"kind": "cut", "value": 200}, day, endDay))
	if renewal.Code != http.StatusCreated {
		t.Fatalf("mk renew: %d", renewal.Code)
	}
	flash := doJSON(t, r, "POST", "/api/campaigns", mkCampaign("限时大促", "timed",
		map[string]any{"kind": "pct", "value": 5000}, day, endDay))
	if flash.Code != http.StatusCreated {
		t.Fatalf("mk timed: %d", flash.Code)
	}
	var flashRow storage.Campaign
	json.Unmarshal(flash.Body.Bytes(), &flashRow)

	// ---- 活动位：进行中三个（均在窗内），过期/停用/未开始不下发 ----
	rec := doPanel(t, r, "buyer-sub", "GET", "/api/panel/campaigns", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("campaigns: %d %s", rec.Code, rec.Body)
	}
	var spots []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &spots); err != nil {
		t.Fatal(err)
	}
	if len(spots) != 3 {
		t.Fatalf("spots = %+v", spots)
	}
	want := map[string]map[string]any{
		"新客首单": {"discount_kind": "cut", "discount_value": 300.0},
		"老客续费": {"discount_kind": "cut", "discount_value": 200.0},
		"限时大促": {"discount_kind": "pct", "discount_value": 5000.0},
	}
	for _, s := range spots {
		if s["state"] != "active" {
			t.Fatalf("spot state = %+v", s)
		}
		w, ok := want[s["name"].(string)]
		if !ok || s["discount_kind"] != w["discount_kind"] || s["discount_value"].(float64) != w["discount_value"] {
			t.Fatalf("spot = %+v", s)
		}
	}
	// 过期活动（窗口整体在过去）：活动位不下发
	if rec = doJSON(t, r, "POST", "/api/campaigns",
		mkCampaign("历史活动", "timed", map[string]any{"kind": "cut", "value": 100},
			now.Add(-2*time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339))); rec.Code != http.StatusCreated {
		t.Fatalf("mk expired: %d", rec.Code)
	}
	rec = doPanel(t, r, "buyer-sub", "GET", "/api/panel/campaigns", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &spots); err != nil {
		t.Fatal(err)
	}
	if len(spots) != 3 {
		t.Fatalf("spots after expired = %+v", spots)
	}

	// ---- 下单取优：惠大者胜出（flash 减 500 > 首单减 300 > 续费减 200） ----
	// 每个场景换 IP 绕下单限流（10 次/分钟）。
	var seq int
	order := func(token, code string) (int, map[string]any) {
		seq++
		body := map[string]any{"batch_id": batch.ID, "provider": "epusdt"}
		if code != "" {
			body["coupon_code"] = code
		}
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/api/panel/orders", &buf)
		req.RemoteAddr = fmt.Sprintf("10.2.0.%d", seq) + ":1234"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// buyer（无已付单）：命中 first_order+timed，flash 惠大 → 减 500 实付 500
	code, out := order("buyer-sub", "")
	if code != http.StatusCreated || out["amount_cents"].(float64) != 500 {
		t.Fatalf("new customer order: %d %v", code, out)
	}
	var ord storage.PaymentOrder
	db.Where("order_no = ?", out["order_no"]).First(&ord)
	if !strings.Contains(ord.PromoSnapshot, `"source":"campaign"`) || !strings.Contains(ord.PromoSnapshot, `"campaign_id":`) {
		t.Fatalf("snapshot = %s", ord.PromoSnapshot)
	}
	if !strings.Contains(ord.PromoSnapshot, `"discount_cents":500`) || !strings.Contains(ord.PromoSnapshot, `"name":"限时大促"`) {
		t.Fatalf("snapshot discount = %s", ord.PromoSnapshot)
	}

	// old（有已付单）：first_order 出局，renew 减 200 vs flash 减 500 → flash 惠大
	if code, out = order("old-sub", ""); code != http.StatusCreated || out["amount_cents"].(float64) != 500 {
		t.Fatalf("renew order: %d %v", code, out)
	}

	// ---- 码 vs 活动取优：码惠大→码落单；活动惠大→码不核销 ----
	// 建码减 600（大于 flash 减 500）
	doJSON(t, r, "POST", "/api/coupons", map[string]any{"code": "BIG600", "kind": "cut", "value": 600})
	if code, out = order("buyer-sub", "BIG600"); code != http.StatusCreated || out["amount_cents"].(float64) != 400 {
		t.Fatalf("coupon wins: %d %v", code, out)
	}
	ord = storage.PaymentOrder{} // GORM First 以非零主键为条件，须重置
	ord = storage.PaymentOrder{} // GORM First 以非零主键为条件，须重置
	db.Where("order_no = ?", out["order_no"]).First(&ord)
	if ord.PromoCode != "BIG600" || !strings.Contains(ord.PromoSnapshot, `"code":"BIG600"`) {
		t.Fatalf("coupon snapshot = %+v %s", ord, ord.PromoSnapshot)
	}
	// 码减 100（小于 flash 减 500）→ 活动胜出，码不核销
	doJSON(t, r, "POST", "/api/coupons", map[string]any{"code": "SMALL100", "kind": "cut", "value": 100})
	if code, out = order("buyer-sub", "SMALL100"); code != http.StatusCreated || out["amount_cents"].(float64) != 500 {
		t.Fatalf("campaign wins: %d %v", code, out)
	}
	ord = storage.PaymentOrder{} // GORM First 以非零主键为条件，须重置
	db.Where("order_no = ?", out["order_no"]).First(&ord)
	if ord.PromoCode != "" || !strings.Contains(ord.PromoSnapshot, `"source":"campaign"`) {
		t.Fatalf("campaign snapshot = %+v %s", ord, ord.PromoSnapshot)
	}
	var small storage.Coupon
	db.Where("code = ?", "SMALL100").First(&small)
	if small.Used != 0 {
		t.Fatalf("losing coupon burned: used=%d", small.Used)
	}

	// ---- 停用全部活动后下单：无促销可命中，回落面价 ----
	var camps []storage.Campaign
	db.Find(&camps)
	for _, c := range camps {
		if rec = doJSON(t, r, "PUT", "/api/campaigns/"+strconv.Itoa(int(c.ID)), map[string]any{
			"name": c.Name, "kind": c.Kind, "rules": jsonStr(map[string]any{"kind": "cut", "value": 1}),
			"starts_at": day, "ends_at": endDay, "enabled": false,
		}); rec.Code != http.StatusOK {
			t.Fatalf("disable %d: %d", c.ID, rec.Code)
		}
	}
	if code, out = order("buyer-sub", ""); code != http.StatusCreated || out["amount_cents"].(float64) != 1000 {
		t.Fatalf("no campaign left: %d %v", code, out)
	}
	ord = storage.PaymentOrder{} // GORM First 以非零主键为条件，须重置
	db.Where("order_no = ?", out["order_no"]).First(&ord)
	if ord.PromoCode != "" || ord.PromoSnapshot != "" {
		t.Fatalf("plain order = %+v %s", ord, ord.PromoSnapshot)
	}
}
