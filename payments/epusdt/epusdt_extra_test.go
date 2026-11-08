package epusdt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/packages/payment"
)

// TestCreateOrderValidation 订单号与金额必填：缺任一即拒绝，不触网。
func TestCreateOrderValidation(t *testing.T) {
	e := newConfigured("http://127.0.0.1:1")
	if _, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "", AmountCents: 100}); err == nil {
		t.Fatal("empty order_no must error")
	}
	if _, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "o1", AmountCents: 0}); err == nil {
		t.Fatal("zero amount must error")
	}
}

// TestCreateOrderRequestError 请求构造失败（非法字符进 URL）：原样上抛。
func TestCreateOrderRequestError(t *testing.T) {
	e := newConfigured("http://bad\nurl.example")
	_, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "o1", AmountCents: 100})
	if err == nil {
		t.Fatal("malformed base URL must error")
	}
}

// TestCreateOrderNetworkError 网关不可达：包一层 create 错误。
func TestCreateOrderNetworkError(t *testing.T) {
	e := newConfigured("http://127.0.0.1:1")
	_, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "o1", AmountCents: 100})
	if err == nil || !strings.Contains(err.Error(), "epusdt create") {
		t.Fatalf("err = %v; want wrapped create error", err)
	}
}

// TestCreateOrderBadJSON 网关回非 JSON：报响应非 JSON。
func TestCreateOrderBadJSON(t *testing.T) {
	srv, _ := newTestGateway(t, http.StatusOK, "<html>not json</html>")
	e := newConfigured(srv.URL)
	_, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "o1", AmountCents: 100})
	if err == nil || !strings.Contains(err.Error(), "响应非 JSON") {
		t.Fatalf("err = %v; want non-JSON error", err)
	}
}

// TestCreateOrderTruncatedBody 响应声明长度与实际不符：读取中断报错。
func TestCreateOrderTruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("{}")) // 只给一部分即断开
	}))
	defer srv.Close()
	e := newConfigured(srv.URL)
	if _, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "o1", AmountCents: 100}); err == nil {
		t.Fatal("truncated body must error")
	}
}

// TestVerifyConfiguredCheck 未配置凭证的实例直接拒回调。
func TestVerifyConfiguredCheck(t *testing.T) {
	if _, err := (&Epusdt{}).Verify(context.Background(), []byte("order_id=o1")); err == nil {
		t.Fatal("unconfigured gateway must reject callbacks")
	}
}

// TestVerifyBadForm 回调体不是表单：报回调非表单。
func TestVerifyBadForm(t *testing.T) {
	e := newConfigured("http://127.0.0.1:1")
	_, err := e.Verify(context.Background(), []byte("%%%%not-a-form%%%%"))
	if err == nil || !strings.Contains(err.Error(), "回调非表单") {
		t.Fatalf("err = %v; want bad-form error", err)
	}
}
