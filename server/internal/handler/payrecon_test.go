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
