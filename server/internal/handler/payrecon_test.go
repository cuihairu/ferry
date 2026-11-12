package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
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
	// 小计：paid 4 笔（o1/o2/o3/o5），金额 1000+2000+3000+0=6000；流水与发放
	// 全表口径（P2 修复后含游离记录）：流水 1000+4000+999=5999、发放 2 笔。
	if out.Summary.PaidOrders != 4 {
		t.Fatalf("paid_orders = %d", out.Summary.PaidOrders)
	}
	if out.Summary.PaidCents != 6000 || out.Summary.TxnCents != 5999 || out.Summary.Grants != 2 {
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

// TestReconcileGuards 覆盖对账 limit 负例/合法值与退款坏体 400。
func TestReconcileGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, q := range []string{"?limit=abc", "?limit=0", "?limit=501"} {
		if rec := doJSON(t, r, "GET", "/api/payments/reconcile"+q, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", q, rec.Code)
		}
	}
	if rec := doJSON(t, r, "GET", "/api/payments/reconcile?limit=5", nil); rec.Code != http.StatusOK {
		t.Fatalf("limit=5: %d %s", rec.Code, rec.Body)
	}
	// 退款坏体 400（note 选填但 JSON 解析失败仍拒）。
	if rec := doRaw(t, r, "POST", "/api/payments/orders/x/refund", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("refund bad json: %d", rec.Code)
	}
}

// TestReconcileDBErrors 覆盖对账与退款取数/落账失败 → 500（gorm 回调注错，
// 对齐既有口径）：订单/流水/发放取数、空库游离记录取数、退款状态流转。
func TestReconcileDBErrors(t *testing.T) {
	scenarios := []struct {
		name   string
		seed   bool // 预置一笔 paid 订单（空库走游离记录分支）
		refund bool // 打退款而非对账
		reg    func(db *gorm.DB)
	}{
		{"orders query", true, false, func(db *gorm.DB) {
			db.Callback().Query().Before("gorm:query").Register("t:recon", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*[]storage.PaymentOrder); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"txns query", true, false, func(db *gorm.DB) {
			db.Callback().Query().Before("gorm:query").Register("t:recon", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*[]storage.PaymentTransaction); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"grants query", true, false, func(db *gorm.DB) {
			db.Callback().Query().Before("gorm:query").Register("t:recon", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*[]storage.Grant); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"orphan txns query", false, false, func(db *gorm.DB) {
			db.Callback().Query().Before("gorm:query").Register("t:recon", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*[]storage.PaymentTransaction); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"orphan grants query", false, false, func(db *gorm.DB) {
			db.Callback().Query().Before("gorm:query").Register("t:recon", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*[]storage.Grant); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"refund update", true, true, func(db *gorm.DB) {
			db.Callback().Update().Before("gorm:update").Register("t:refund", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Model.(*storage.PaymentOrder); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			r, db := newTestRouterWithDB(t)
			if sc.seed {
				if err := db.Create(&storage.PaymentOrder{
					OrderNo: "x-o1", UserID: 1, Provider: "epusdt",
					AmountCents: 1000, Product: "p", Status: "paid",
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			sc.reg(db)
			var rec *httptest.ResponseRecorder
			if sc.refund {
				rec = doJSON(t, r, "POST", "/api/payments/orders/x-o1/refund", map[string]any{"note": "n"})
			} else {
				rec = doJSON(t, r, "GET", "/api/payments/reconcile", nil)
			}
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("%s: %d %s", sc.name, rec.Code, rec.Body)
			}
		})
	}
}

// TestRefundRowLoadFail 覆盖退款成功后三账视图读不到订单的兜底分支（注错
// 只拦读取不拦更新）：200 且 {"ok":true}，即状态流转确已落库。
func TestRefundRowLoadFail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: "y-o1", UserID: 1, Provider: "epusdt",
		AmountCents: 1000, Product: "p", Status: "paid",
	}).Error; err != nil {
		t.Fatal(err)
	}
	db.Callback().Query().Before("gorm:query").Register("t:refundload", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.PaymentOrder); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	rec := doJSON(t, r, "POST", "/api/payments/orders/y-o1/refund", map[string]any{"note": "n"})
	if rec.Code != http.StatusOK || rec.Body.String() != `{"ok":true}` {
		t.Fatalf("refund with load fail: %d %s", rec.Code, rec.Body)
	}
}

// TestRefundRevokesGrants 覆盖 P2「refund 不回收权益」修复：退款在同一事务
// 内扣回订单发放过的权益——add_quota 从当前余额扣回（下限 0 不记负数）、
// extend_days 从当前到期扣回天数；发放记录留档不变，并按用户落一条站内信。
// 二次退款 409 后权益不再变动（paid 前置挡住重复回收）。
func TestRefundRevokesGrants(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	now := time.Now()
	// 三名用户：u1 配额充足（5GiB 发 2GiB）、u2 有到期（+40 天发 30 天）、
	// u3 配额不足（1GiB 发 2GiB，扣到 0 不记负）。
	for i, name := range []string{"rev-quota", "rev-days", "rev-floor"} {
		u := storage.User{Username: name, SubToken: name + "-sub", QuotaBytes: int64(5-i*2) << 30, Enabled: true}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	var u1, u2, u3 storage.User
	db.Where("username = ?", "rev-quota").First(&u1)
	db.Where("username = ?", "rev-days").First(&u2)
	db.Where("username = ?", "rev-floor").First(&u3)
	if err := db.Model(&storage.User{}).Where("id = ?", u2.ID).
		Update("expires_at", now.Add(40*24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	seed := []storage.PaymentOrder{
		{OrderNo: "rv-o1", UserID: u1.ID, Provider: "epusdt", AmountCents: 1000, Product: "p", Status: "paid", PaidAt: &now, GrantType: "add_quota", GrantValue: 2 << 30},
		{OrderNo: "rv-o2", UserID: u2.ID, Provider: "epusdt", AmountCents: 2000, Product: "p", Status: "paid", PaidAt: &now, GrantType: "extend_days", GrantValue: 30},
		{OrderNo: "rv-o3", UserID: u3.ID, Provider: "epusdt", AmountCents: 3000, Product: "p", Status: "paid", PaidAt: &now, GrantType: "add_quota", GrantValue: 2 << 30},
	}
	for i := range seed {
		if err := db.Create(&seed[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	grants := []storage.Grant{
		{OrderNo: "rv-o1", UserID: u1.ID, GrantType: "add_quota", GrantValue: 2 << 30},
		{OrderNo: "rv-o2", UserID: u2.ID, GrantType: "extend_days", GrantValue: 30},
		{OrderNo: "rv-o3", UserID: u3.ID, GrantType: "add_quota", GrantValue: 2 << 30},
	}
	for i := range grants {
		if err := db.Create(&grants[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	for _, no := range []string{"rv-o1", "rv-o2", "rv-o3"} {
		if rec := doJSON(t, r, "POST", "/api/payments/orders/"+no+"/refund", map[string]any{"note": "x"}); rec.Code != http.StatusOK {
			t.Fatalf("refund %s: %d %s", no, rec.Code, rec.Body)
		}
	}

	// 配额扣回：5GiB-2GiB=3GiB、1GiB-2GiB 下限 0；时长扣回：40 天-30 天≈10 天。
	var after1, after2, after3 storage.User
	db.First(&after1, u1.ID)
	db.First(&after2, u2.ID)
	db.First(&after3, u3.ID)
	if after1.QuotaBytes != 3<<30 {
		t.Fatalf("quota revoke = %d, want %d", after1.QuotaBytes, 3<<30)
	}
	if after3.QuotaBytes != 0 {
		t.Fatalf("quota floor = %d, want 0", after3.QuotaBytes)
	}
	if after2.ExpiresAt == nil {
		t.Fatal("expires_at should remain set")
	}
	if d := time.Until(*after2.ExpiresAt); d < 9*24*time.Hour || d > 11*24*time.Hour {
		t.Fatalf("expires_at until = %v, want ~10d", d)
	}

	// 发放记录留档不变（回收不改三账发放行）。
	var grantRows int64
	db.Model(&storage.Grant{}).Where("order_no IN ?", []string{"rv-o1", "rv-o2", "rv-o3"}).Count(&grantRows)
	if grantRows != 3 {
		t.Fatalf("grant rows = %d, want 3", grantRows)
	}
	// 每用户一条回收站内信。
	var notes int64
	db.Model(&storage.Notification{}).Where("title = ?", "订单已退款，对应权益已回收").Count(&notes)
	if notes != 3 {
		t.Fatalf("notifications = %d, want 3", notes)
	}

	// 二次退款 409：权益不再扣（paid 前置挡住重复回收）。
	if rec := doJSON(t, r, "POST", "/api/payments/orders/rv-o1/refund", map[string]any{"note": "again"}); rec.Code != http.StatusConflict {
		t.Fatalf("refund again: %d", rec.Code)
	}
	var re storage.User
	db.First(&re, u1.ID)
	if re.QuotaBytes != 3<<30 {
		t.Fatalf("quota after double refund = %d, want unchanged %d", re.QuotaBytes, 3<<30)
	}
}

// TestReconcileSummaryWholeLedger 覆盖 P2「reconcile 统计口径错」修复：小计
// 不再随 limit 页截断——订单超过一页时 summary 仍按全表聚合（实收/退款按
// 状态分桶、流水与发放全表合计），页内行数照常按 limit 返回。
func TestReconcileSummaryWholeLedger(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	now := time.Now()
	// 250 单：200 paid @100 分 + 50 refunded @200 分，各带一笔流水与发放。
	const total = 250
	orders := make([]storage.PaymentOrder, 0, total)
	txns := make([]storage.PaymentTransaction, 0, total)
	grants := make([]storage.Grant, 0, total)
	for i := 0; i < total; i++ {
		status, amount := "paid", int64(100)
		if i >= 200 {
			status, amount = "refunded", 200
		}
		no := fmt.Sprintf("w-o%03d", i)
		orders = append(orders, storage.PaymentOrder{OrderNo: no, UserID: 1, Provider: "epusdt", AmountCents: amount, Status: status, PaidAt: &now})
		txns = append(txns, storage.PaymentTransaction{OrderNo: no, Provider: "epusdt", ExternalID: no, AmountCents: amount})
		grants = append(grants, storage.Grant{OrderNo: no, UserID: 1, GrantType: "add_quota", GrantValue: 1})
	}
	if err := db.Create(&orders).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&txns).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&grants).Error; err != nil {
		t.Fatal(err)
	}

	// limit=100：页内 100 行，summary 覆盖全表 250 单（修复前只统计页内）。
	rec := doJSON(t, r, "GET", "/api/payments/reconcile?limit=100", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reconcile: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Orders  []mapoke `json:"orders"`
		Summary struct {
			PaidOrders     int64 `json:"paid_orders"`
			PaidCents      int64 `json:"paid_cents"`
			RefundedOrders int64 `json:"refunded_orders"`
			RefundedCents  int64 `json:"refunded_cents"`
			TxnCents       int64 `json:"txn_cents"`
			Grants         int64 `json:"grants"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Orders) != 100 {
		t.Fatalf("page rows = %d, want 100", len(out.Orders))
	}
	s := out.Summary
	if s.PaidOrders != 200 || s.PaidCents != 200*100 {
		t.Fatalf("paid summary = %+v", s)
	}
	if s.RefundedOrders != 50 || s.RefundedCents != 50*200 {
		t.Fatalf("refunded summary = %+v", s)
	}
	if s.TxnCents != 200*100+50*200 {
		t.Fatalf("txn cents = %d", s.TxnCents)
	}
	if s.Grants != 250 {
		t.Fatalf("grants = %d", s.Grants)
	}
}
