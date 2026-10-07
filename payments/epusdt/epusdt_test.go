package epusdt

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/packages/payment"
)

// newTestGateway 起一个 epusdt 口径的假网关，记录收到的表单与响应体。
func newTestGateway(t *testing.T, status int, respBody string) (*httptest.Server, *[]byte) {
	t.Helper()
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != createPath {
			http.NotFound(w, r)
			return
		}
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// newConfigured 装配指向假网关的客户端。
func newConfigured(srvURL string) *Epusdt {
	return &Epusdt{
		baseURL:   srvURL,
		token:     "secret-token",
		notifyURL: "https://panel.example.com/api/pay/epusdt/notify",
		client:    &http.Client{},
	}
}

// TestSign 覆盖 epusdt 签名规则：ASCII 升序、空值跳过、token 直拼、
// signature 自身不参与；与手算 md5 对照。
func TestSign(t *testing.T) {
	e := newConfigured("http://pay.example.com")
	vals := url.Values{
		"order_id":   {"o-1"},
		"amount":     {"10.00"},
		"notify_url": {"https://panel/notify"},
		"empty":      {""},
		"signature":  {"self-not-in-sign"},
	}
	want := md5hex("amount=10.00&notify_url=https://panel/notify&order_id=o-1" + "secret-token")
	if got := e.sign(vals); got != want {
		t.Fatalf("sign = %s, want %s", got, want)
	}
	// 签名与 token 绑定
	e2 := newConfigured("http://pay.example.com")
	e2.token = "other"
	if e2.sign(vals) == want {
		t.Fatal("sign should depend on token")
	}
}

// TestCreateOrder 覆盖下单主通路：表单参数（含签名）按协议发送、
// 响应换算为 Receipt；另覆盖网关业务失败与未配置两条错误路径。
func TestCreateOrder(t *testing.T) {
	srv, got := newTestGateway(t, http.StatusOK,
		`{"statusCode":200,"message":"ok","data":{"order_id":"o-1","trade_id":"t-9",`+
			`"payment_url":"https://pay.example.com/pay/t-9","actual_amount":"1.52","expiration_timestamp":1767225600}}`)
	e := newConfigured(srv.URL)

	rcpt, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "o-1", AmountCents: 1000})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if rcpt.Provider != "epusdt" || rcpt.ExternalID != "t-9" || !strings.Contains(rcpt.PayURL, "/pay/t-9") {
		t.Fatalf("receipt = %+v", rcpt)
	}
	if rcpt.ExpiresAt.IsZero() {
		t.Fatal("expiration_timestamp 应换算为 ExpiresAt")
	}
	// 网关侧收到的表单应带合法签名（用同规则验算）
	vals := mustParseQuery(t, string(*got))
	eRef := newConfigured(srv.URL)
	if eRef.sign(vals) != vals.Get("signature") {
		t.Fatalf("form signature invalid: %v", vals)
	}
	if vals.Get("amount") != "10.00" || vals.Get("order_id") != "o-1" {
		t.Fatalf("form params = %v", vals)
	}

	// 网关业务失败 → 报错
	srvBad, _ := newTestGateway(t, http.StatusOK, `{"statusCode":400,"message":"金额低于起付"}`)
	if _, err := newConfigured(srvBad.URL).CreateOrder(context.Background(), payment.Order{OrderNo: "o-2", AmountCents: 100}); err == nil {
		t.Fatal("want error on statusCode 400")
	}

	// 未配置 → 报错
	if _, err := (&Epusdt{}).CreateOrder(context.Background(), payment.Order{OrderNo: "o", AmountCents: 1}); err == nil {
		t.Fatal("want error when unconfigured")
	}
}

// TestVerify 覆盖回调验签：合法回调（status=2）换算 Callback；
// 篡改签名 / 非成功状态 / 金额非法各自报错。
func TestVerify(t *testing.T) {
	e := newConfigured("http://pay.example.com")
	// 用网关口径构造合法回调
	sig := md5hex("amount=10.00&order_id=o-1&status=2&trade_id=t-9" + "secret-token")
	raw := "amount=10.00&order_id=o-1&status=2&trade_id=t-9&signature=" + sig

	cb, err := e.Verify(context.Background(), []byte(raw))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if cb.OrderNo != "o-1" || cb.ExternalID != "t-9" || cb.AmountCents != 1000 {
		t.Fatalf("callback = %+v", cb)
	}
	if len(cb.Raw) == 0 {
		t.Fatal("raw 应保留备查")
	}

	// 篡改签名
	bad := strings.Replace(raw, sig, strings.Repeat("0", 32), 1)
	if _, err := e.Verify(context.Background(), []byte(bad)); err == nil {
		t.Fatal("want error on tampered signature")
	}
	// 非成功状态
	notPaid := "amount=10.00&order_id=o-1&status=1&trade_id=t-9&signature=" +
		md5hex("amount=10.00&order_id=o-1&status=1&trade_id=t-9"+"secret-token")
	if _, err := e.Verify(context.Background(), []byte(notPaid)); err == nil {
		t.Fatal("want error on status=1")
	}
	// 金额非法
	badAmt := "amount=x&order_id=o-1&status=2&trade_id=t-9&signature=" +
		md5hex("amount=x&order_id=o-1&status=2&trade_id=t-9"+"secret-token")
	if _, err := e.Verify(context.Background(), []byte(badAmt)); err == nil {
		t.Fatal("want error on bad amount")
	}
}

// TestYuanCentsRoundTrip 覆盖金额换算（两位小数元 ↔ 分）。
func TestYuanCentsRoundTrip(t *testing.T) {
	for cents := range map[int64]bool{1: true, 99: true, 1000: true, 12345: true} {
		back, err := yuanToCents(centsToYuan(cents))
		if err != nil || back != cents {
			t.Fatalf("roundtrip %d: %d %v", cents, back, err)
		}
	}
	if v, err := yuanToCents("10.005"); err != nil || v != 1001 { // 四舍五入到分
		t.Fatalf("10.005 → %d %v, want 1001", v, err)
	}
}

// ---- 测试小工具 ----

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func mustParseQuery(t *testing.T, s string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(s)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	return v
}
