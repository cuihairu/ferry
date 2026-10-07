package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestReconcile 覆盖三账对账（PAY-9）：按订单分组、缺失环节标注
// （paid 缺流水 / paid 缺发放 / pending 有流水）、游离记录、总额小计。
func TestReconcile(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	now := time.Now()

	// o1：三账齐全（paid+流水+发放）→ 无缺失
	// o2：paid 无流水 →「缺支付流水」
	// o3：paid 带发放口径但无发放 →「缺发放」
	// o4：pending 却有流水 →「有流水未结算」
	seed := []storage.PaymentOrder{
		{OrderNo: "r-o1", UserID: 1, Provider: "epusdt", AmountCents: 1000, Product: "p1", Status: "paid", PaidAt: &now, GrantType: "add_quota", GrantValue: 1},
		{OrderNo: "r-o2", UserID: 1, Provider: "epusdt", AmountCents: 2000, Product: "p2", Status: "paid", PaidAt: &now},
		{OrderNo: "r-o3", UserID: 1, Provider: "epusdt", AmountCents: 3000, Product: "p3", Status: "paid", PaidAt: &now, GrantType: "extend_days", GrantValue: 30},
		{OrderNo: "r-o4", UserID: 1, Provider: "epusdt", AmountCents: 4000, Product: "p4", Status: "pending"},
		{OrderNo: "r-o5", UserID: 1, Provider: "card", AmountCents: 0, Product: "卡密", Status: "paid", PaidAt: &now},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	rows := []storage.PaymentTransaction{
		{OrderNo: "r-o1", Provider: "epusdt", ExternalID: "t1", AmountCents: 1000, OccurredAt: now},
		{OrderNo: "r-o4", Provider: "epusdt", ExternalID: "t4", AmountCents: 4000, OccurredAt: now},
		// 游离流水：订单不存在
		{OrderNo: "r-ghost", Provider: "epusdt", ExternalID: "tg", AmountCents: 999, OccurredAt: now},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	grants := []storage.Grant{
		{OrderNo: "r-o1", UserID: 1, GrantType: "add_quota", GrantValue: 1},
		// 游离发放：订单不存在
		{OrderNo: "r-ghost2", UserID: 1, GrantType: "extend_days", GrantValue: 7},
	}
	for i := range grants {
		if err := db.Create(&grants[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	rec := doJSON(t, r, "GET", "/api/payments/reconcile", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reconcile: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Orders []struct {
			OrderNo      string   `json:"order_no"`
			Status       string   `json:"status"`
			Transactions []mapoke `json:"transactions"`
			Grants       []mapoke `json:"grants"`
			Missing      []string `json:"missing"`
		} `json:"orders"`
		Orphans []struct {
			Kind    string `json:"kind"`
			OrderNo string `json:"order_no"`
		} `json:"orphans"`
		Summary struct {
			PaidOrders int64 `json:"paid_orders"`
			PaidCents  int64 `json:"paid_cents"`
			TxnCents   int64 `json:"txn_cents"`
			Grants     int64 `json:"grants"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	byNo := map[string]int{}
	for i, o := range out.Orders {
		byNo[o.OrderNo] = i
	}
	get := func(no string) *struct {
		OrderNo      string   `json:"order_no"`
		Status       string   `json:"status"`
		Transactions []mapoke `json:"transactions"`
		Grants       []mapoke `json:"grants"`
		Missing      []string `json:"missing"`
	} {
		i, ok := byNo[no]
		if !ok {
			t.Fatalf("order %s missing", no)
		}
		return &out.Orders[i]
	}
	if m := get("r-o1").Missing; len(m) != 0 {
		t.Fatalf("r-o1 should be clean, missing=%v", m)
	}
	if len(get("r-o1").Transactions) != 1 || len(get("r-o1").Grants) != 1 {
		t.Fatal("r-o1 should carry txn+grant")
	}
	if m := get("r-o2").Missing; len(m) != 1 || m[0] != "缺支付流水" {
		t.Fatalf("r-o2 missing=%v", get("r-o2").Missing)
	}
	if m := get("r-o3").Missing; len(m) != 2 || m[0] != "缺支付流水" || m[1] != "缺发放" {
		t.Fatalf("r-o3 missing=%v, want 缺支付流水+缺发放", get("r-o3").Missing)
	}
	if m := get("r-o4").Missing; len(m) != 1 || m[0] != "有流水未结算" {
		t.Fatalf("r-o4 missing=%v", get("r-o4").Missing)
	}
	// card 类无发放口径不标缺发放，但 paid 无流水仍标缺流水
	if m := get("r-o5").Missing; len(m) != 1 || m[0] != "缺支付流水" {
		t.Fatalf("r-o5 missing=%v, want 缺支付流水", get("r-o5").Missing)
	}
	// 游离记录两条都报
	if len(out.Orphans) != 2 {
		t.Fatalf("orphans = %d, want 2: %+v", len(out.Orphans), out.Orphans)
	}
	// 小计：paid 4 笔（o1/o2/o3/o5），金额 1000+2000+3000+0=6000；流水 1000+4000=5000；发放 1 笔
	if out.Summary.PaidOrders != 4 {
		t.Fatalf("paid_orders = %d", out.Summary.PaidOrders)
	}
	if out.Summary.PaidCents != 6000 || out.Summary.TxnCents != 5000 || out.Summary.Grants != 1 {
		t.Fatalf("summary = %+v", out.Summary)
	}
}

// mapoke 是测试里只看条数的泛型行。
type mapoke = map[string]any

// TestOrderRefundAndDetail 覆盖 OD-2：退款流转（仅 paid→refunded，409 否则）、
// 三账详情接口、reconcile 退款小计与「已退款但缺支付流水」缺失检查。
func TestOrderRefundAndDetail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	now := time.Now()

	// d-o1：paid，三账齐全（待退）；
	// d-o2：pending（不可退）；
	// d-o3：paid 无流水（退后应标「已退款但缺支付流水」）。
	seed := []storage.PaymentOrder{
		{OrderNo: "d-o1", UserID: 1, Provider: "epusdt", AmountCents: 1000, Product: "p1", Status: "paid", PaidAt: &now, GrantType: "add_quota", GrantValue: 1},
		{OrderNo: "d-o2", UserID: 1, Provider: "epusdt", AmountCents: 2000, Product: "p2", Status: "pending"},
		{OrderNo: "d-o3", UserID: 1, Provider: "epusdt", AmountCents: 3000, Product: "p3", Status: "paid", PaidAt: &now},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&storage.PaymentTransaction{OrderNo: "d-o1", Provider: "epusdt", ExternalID: "t1", AmountCents: 1000, OccurredAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.Grant{OrderNo: "d-o1", UserID: 1, GrantType: "add_quota", GrantValue: 1}).Error; err != nil {
		t.Fatal(err)
	}

	// 详情：三账齐全
	rec := doJSON(t, r, "GET", "/api/payments/orders/d-o1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail d-o1: %d %s", rec.Code, rec.Body)
	}
	var row struct {
		OrderNo      string   `json:"order_no"`
		Status       string   `json:"status"`
		Transactions []mapoke `json:"transactions"`
		Grants       []mapoke `json:"grants"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.OrderNo != "d-o1" || row.Status != "paid" || len(row.Transactions) != 1 || len(row.Grants) != 1 {
		t.Fatalf("detail = %+v", row)
	}
	// 详情：订单不存在 → 404
	if rec := doJSON(t, r, "GET", "/api/payments/orders/nope", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("detail missing: %d", rec.Code)
	}

	// 退款：paid → refunded，回写退款留痕
	rec = doJSON(t, r, "POST", "/api/payments/orders/d-o1/refund", map[string]any{"note": "C-20261008-001"})
	if rec.Code != http.StatusOK {
		t.Fatalf("refund d-o1: %d %s", rec.Code, rec.Body)
	}
	var order storage.PaymentOrder
	if err := db.Where("order_no = ?", "d-o1").First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != "refunded" || order.RefundAt == nil || order.RefundNote != "C-20261008-001" {
		t.Fatalf("d-o1 after refund = %s/%v/%q", order.Status, order.RefundAt, order.RefundNote)
	}

	// 二次退款 → 409；pending 退款 → 409；订单不存在 → 404
	if rec := doJSON(t, r, "POST", "/api/payments/orders/d-o1/refund", map[string]any{"note": "again"}); rec.Code != http.StatusConflict {
		t.Fatalf("refund again: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/payments/orders/d-o2/refund", map[string]any{"note": "x"}); rec.Code != http.StatusConflict {
		t.Fatalf("refund pending: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/payments/orders/nope/refund", map[string]any{"note": "x"}); rec.Code != http.StatusConflict {
		t.Fatalf("refund missing: %d", rec.Code)
	}

	// d-o3 也退掉（无流水），reconcile 应出退款小计与「已退款但缺支付流水」
	if rec := doJSON(t, r, "POST", "/api/payments/orders/d-o3/refund", nil); rec.Code != http.StatusOK {
		t.Fatalf("refund d-o3: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/payments/reconcile", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reconcile: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Orders []struct {
			OrderNo string   `json:"order_no"`
			Status  string   `json:"status"`
			Missing []string `json:"missing"`
		} `json:"orders"`
		Summary struct {
			PaidOrders    int64 `json:"paid_orders"`
			PaidCents     int64 `json:"paid_cents"`
			RefundedOrder int64 `json:"refunded_orders"`
			RefundedCents int64 `json:"refunded_cents"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	byNo := map[string]struct {
		Status  string
		Missing []string
	}{}
	for _, o := range out.Orders {
		byNo[o.OrderNo] = struct {
			Status  string
			Missing []string
		}{o.Status, o.Missing}
	}
	if byNo["d-o1"].Status != "refunded" || len(byNo["d-o1"].Missing) != 0 {
		t.Fatalf("d-o1 = %+v", byNo["d-o1"])
	}
	if m := byNo["d-o3"].Missing; len(m) != 1 || m[0] != "已退款但缺支付流水" {
		t.Fatalf("d-o3 missing = %v", byNo["d-o3"].Missing)
	}
	// 小计：退款 2 笔（1000+3000=4000）；paid 归零（d-o2 还是 pending）
	if out.Summary.RefundedOrder != 2 || out.Summary.RefundedCents != 4000 {
		t.Fatalf("summary = %+v", out.Summary)
	}
	if out.Summary.PaidOrders != 0 || out.Summary.PaidCents != 0 {
		t.Fatalf("summary = %+v", out.Summary)
	}
}
