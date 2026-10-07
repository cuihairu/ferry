package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// stubGateway 是下单可用的假网关：收银台与交易号按订单号拼出。
type stubGateway struct{}

func (stubGateway) Name() string { return "epusdt" }
func (stubGateway) CreateOrder(_ context.Context, o payment.Order) (payment.Receipt, error) {
	return payment.Receipt{
		Provider: "epusdt", PayURL: "https://pay.example.com/go?order=" + o.OrderNo,
		ExternalID: "trade-" + o.OrderNo, ExpiresAt: time.Now().Add(30 * time.Minute),
	}, nil
}
func (stubGateway) Verify(context.Context, []byte) (payment.Callback, error) {
	return payment.Callback{}, nil // 下单测试不验回调，结算走 stubEpusdt 覆盖注册
}

// TestPanelOrderFlow 覆盖门户在线下单闭环（PAY-11）：商品可见性
// （设价上架，免费/过期/售罄/不存在不可售）、渠道不可用、上游失败 502
// 不落订单、成功下单落 pending 锚点、回调结算后状态查询为 paid 且发放到位。
func TestPanelOrderFlow(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	for _, name := range []string{"buyer", "other"} {
		if err := db.Create(&storage.User{
			Username: name, SubToken: name + "-sub", QuotaBytes: 1 << 30, Enabled: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	batches := []storage.CardBatch{
		{Name: "50GB 流量包", GrantType: "add_quota", GrantValue: 50 << 30, PriceCents: 1000, Total: 10, CreatedBy: "admin"},
		{Name: "仅兑换包", GrantType: "add_quota", GrantValue: 1 << 30, PriceCents: 0, Total: 10, CreatedBy: "admin"},
		{Name: "过期包", GrantType: "extend_days", GrantValue: 30, PriceCents: 500, Total: 10, CreatedBy: "admin", ExpiredAt: &past},
		{Name: "售罄包", GrantType: "extend_days", GrantValue: 30, PriceCents: 500, Total: 10, CreatedBy: "admin"},
	}
	for i := range batches {
		if err := db.Create(&batches[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	codes := []storage.CardCode{
		{BatchID: batches[0].ID, Code: "P11-SELL-1", Status: "unused"},
		{BatchID: batches[1].ID, Code: "P11-FREE-1", Status: "unused"},
		{BatchID: batches[2].ID, Code: "P11-EXP-1", Status: "unused"},
		// batches[3] 无码 → 售罄
	}
	for i := range codes {
		if err := db.Create(&codes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	sell := batches[0]
	post := func(path string, body any) *int {
		rec := doPanel(t, r, "buyer-sub", "POST", path, body)
		return &rec.Code
	}

	// ---- 商品列表：只下发设价、未过期、有余量的批次 ----
	payment.Register(stubGateway{})
	rec := doPanel(t, r, "buyer-sub", "GET", "/api/panel/products", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("products: %d %s", rec.Code, rec.Body)
	}
	var products []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &products); err != nil {
		t.Fatal(err)
	}
	if len(products) != 1 || products[0]["id"].(float64) != float64(sell.ID) {
		t.Fatalf("products = %+v, want only batch %d", products, sell.ID)
	}

	// ---- 下单前置校验 ----
	if got := *post("/api/panel/orders", map[string]any{"batch_id": batches[3].ID, "provider": "epusdt"}); got != http.StatusBadRequest {
		t.Fatalf("sold-out should 400: %d", got)
	}
	if got := *post("/api/panel/orders", map[string]any{"batch_id": batches[1].ID, "provider": "epusdt"}); got != http.StatusBadRequest {
		t.Fatalf("unsellable (price 0) should 400: %d", got)
	}
	if got := *post("/api/panel/orders", map[string]any{"batch_id": batches[2].ID, "provider": "epusdt"}); got != http.StatusBadRequest {
		t.Fatalf("expired should 400: %d", got)
	}
	if got := *post("/api/panel/orders", map[string]any{"batch_id": 99999, "provider": "epusdt"}); got != http.StatusBadRequest {
		t.Fatalf("missing batch should 400: %d", got)
	}
	if got := *post("/api/panel/orders", map[string]any{"batch_id": sell.ID, "provider": "nosuch"}); got != http.StatusBadRequest {
		t.Fatalf("unknown provider should 400: %d", got)
	}

	// ---- 上游网关失败 → 502，不落订单 ----
	payment.Register(stubEpusdt{err: errors.New("网关不可用")})
	if got := *post("/api/panel/orders", map[string]any{"batch_id": sell.ID, "provider": "epusdt"}); got != http.StatusBadGateway {
		t.Fatalf("upstream failure should 502: %d", got)
	}
	var orderCount int64
	db.Model(&storage.PaymentOrder{}).Count(&orderCount)
	if orderCount != 0 {
		t.Fatalf("failed order persisted: %d", orderCount)
	}

	// ---- 成功下单：pending 锚点 + 收银台 ----
	payment.Register(stubGateway{})
	rec = doPanel(t, r, "buyer-sub", "POST", "/api/panel/orders",
		map[string]any{"batch_id": sell.ID, "provider": "epusdt"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create order: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		OrderNo string `json:"order_no"`
		PayURL  string `json:"pay_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.OrderNo == "" || !strings.HasPrefix(created.PayURL, "https://pay.example.com/") {
		t.Fatalf("receipt = %+v", created)
	}
	var order storage.PaymentOrder
	if err := db.Where("order_no = ?", created.OrderNo).First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != "pending" || order.AmountCents != 1000 || order.GrantType != "add_quota" || order.GrantValue != 50<<30 {
		t.Fatalf("pending order = %+v", order)
	}

	// ---- 结算前状态查询：pending ----
	rec = doPanel(t, r, "buyer-sub", "GET", "/api/panel/orders/"+created.OrderNo, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"pending"`) {
		t.Fatalf("status before pay: %d %s", rec.Code, rec.Body)
	}

	// ---- 回调结算 → paid + 自动发放 ----
	payment.Register(stubEpusdt{cb: payment.Callback{
		OrderNo: created.OrderNo, ExternalID: "tx-p11", AmountCents: 1000,
		PaidAt: time.Now(), Raw: []byte("order_id=" + created.OrderNo + "&status=2"),
	}})
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusOK {
		t.Fatalf("notify: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	db.Where("sub_token = ?", "buyer-sub").First(&u)
	if u.QuotaBytes != (1<<30)+(50<<30) {
		t.Fatalf("quota = %d, want +50GB", u.QuotaBytes)
	}

	// ---- 结算后状态查询：paid + 发放明细 ----
	rec = doPanel(t, r, "buyer-sub", "GET", "/api/panel/orders/"+created.OrderNo, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"paid"`) {
		t.Fatalf("status after pay: %d %s", rec.Code, rec.Body)
	}
	var detail struct {
		Grants []map[string]any `json:"grants"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Grants) != 1 || detail.Grants[0]["grant_type"] != "add_quota" {
		t.Fatalf("grants = %+v", detail.Grants)
	}

	// ---- 越权与不存在：他人令牌/任意订单号统一 404 ----
	if rec := doPanel(t, r, "other-sub", "GET", "/api/panel/orders/"+created.OrderNo, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other user's order should 404: %d", rec.Code)
	}
	if rec := doPanel(t, r, "buyer-sub", "GET", "/api/panel/orders/no-such", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown order should 404: %d", rec.Code)
	}
}
