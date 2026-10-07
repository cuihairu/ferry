package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// stubEpusdt 是受控的假 epusdt Provider：Verify 返回预设结论。
type stubEpusdt struct {
	cb  payment.Callback
	err error
}

func (s stubEpusdt) Name() string { return "epusdt" }
func (s stubEpusdt) CreateOrder(context.Context, payment.Order) (payment.Receipt, error) {
	return payment.Receipt{}, errors.New("stub 未实现下单")
}
func (s stubEpusdt) Verify(context.Context, []byte) (payment.Callback, error) {
	return s.cb, s.err
}

// TestEpusdtNotify 覆盖回调结算（PAY-8）：验签失败 400、对单校验
// （不存在/金额不符/渠道不符）、成功结算三账+发放、重复回调幂等。
func TestEpusdtNotify(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if err := db.Create(&storage.User{
		Username: "payer", SubToken: "payer-sub", QuotaBytes: 1 << 30, Enabled: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: "epusdt-o-1", UserID: 1, Provider: "epusdt", AmountCents: 1000,
		Product: "50GB 流量包", Status: "pending",
		GrantType: "add_quota", GrantValue: 5 << 30,
	}).Error; err != nil {
		t.Fatal(err)
	}

	paidAt := time.Now()
	okCb := payment.Callback{
		OrderNo: "epusdt-o-1", ExternalID: "tx-9", AmountCents: 1000,
		PaidAt: paidAt, Raw: []byte("amount=10.00&order_id=epusdt-o-1&status=2"),
	}

	// 验签失败 → 400，不落账
	payment.Register(stubEpusdt{err: errors.New("签名不符")})
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad signature should 400: %d %s", rec.Code, rec.Body)
	}

	// 订单不存在 → 400
	payment.Register(stubEpusdt{cb: payment.Callback{OrderNo: "no-such", ExternalID: "tx-x", AmountCents: 1000}})
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing order should 400: %d %s", rec.Code, rec.Body)
	}

	// 金额不符 → 400，订单保持 pending
	misCb := okCb
	misCb.AmountCents = 1
	payment.Register(stubEpusdt{cb: misCb})
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("amount mismatch should 400: %d %s", rec.Code, rec.Body)
	}

	// 渠道不符 → 400
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: "card-o-2", UserID: 1, Provider: "card", Status: "pending",
	}).Error; err != nil {
		t.Fatal(err)
	}
	cardCb := okCb
	cardCb.OrderNo = "card-o-2"
	payment.Register(stubEpusdt{cb: cardCb})
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("provider mismatch should 400: %d %s", rec.Code, rec.Body)
	}

	// 成功结算：流水+订单 paid+发放
	payment.Register(stubEpusdt{cb: okCb})
	rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("settle: %d %s", rec.Code, rec.Body)
	}
	var txnCount, grantCount int64
	db.Model(&storage.PaymentTransaction{}).Count(&txnCount)
	db.Model(&storage.Grant{}).Count(&grantCount)
	if txnCount != 1 || grantCount != 1 {
		t.Fatalf("rows = txns %d grants %d, want 1/1", txnCount, grantCount)
	}
	var order storage.PaymentOrder
	if err := db.Where("order_no = ?", "epusdt-o-1").First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != "paid" || order.PaidAt == nil {
		t.Fatalf("order = %+v, want paid", order)
	}
	var u storage.User
	db.First(&u, 1)
	if u.QuotaBytes != (1<<30)+(5<<30) {
		t.Fatalf("quota = %d, want +5GB", u.QuotaBytes)
	}
	var grant storage.Grant
	db.First(&grant)
	var snap map[string]any
	if err := json.Unmarshal([]byte(grant.Snapshot), &snap); err != nil {
		t.Fatal(err)
	}
	if snap["trade_id"] != "tx-9" {
		t.Fatalf("snapshot missing trade_id: %v", snap)
	}

	// 重复回调（同 trade_id）→ 200 幂等，不重复落账
	if rec := doJSON(t, r, "POST", "/api/pay/epusdt/notify", nil); rec.Code != http.StatusOK {
		t.Fatalf("replay should 200: %d %s", rec.Code, rec.Body)
	}
	db.Model(&storage.PaymentTransaction{}).Count(&txnCount)
	db.Model(&storage.Grant{}).Count(&grantCount)
	if txnCount != 1 || grantCount != 1 {
		t.Fatalf("replay duplicated rows: txns %d grants %d", txnCount, grantCount)
	}
	var u2 storage.User
	db.First(&u2, 1)
	if u2.QuotaBytes != u.QuotaBytes {
		t.Fatalf("replay re-granted: %d → %d", u.QuotaBytes, u2.QuotaBytes)
	}
}
