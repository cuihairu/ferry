package epusdt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/packages/payment"
)

// TestCreateOrderValidation covers order number and amount being required: either missing is rejected, no network touched.
func TestCreateOrderValidation(t *testing.T) {
	e := newConfigured("http://127.0.0.1:1")
	if _, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "", AmountCents: 100}); err == nil {
		t.Fatal("empty order_no must error")
	}
	if _, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "o1", AmountCents: 0}); err == nil {
		t.Fatal("zero amount must error")
	}
}

// TestCreateOrderRequestError covers request construction failing (illegal characters in the URL): passed through as-is.
func TestCreateOrderRequestError(t *testing.T) {
	e := newConfigured("http://bad\nurl.example")
	_, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "o1", AmountCents: 100})
	if err == nil {
		t.Fatal("malformed base URL must error")
	}
}

// TestCreateOrderNetworkError covers an unreachable gateway: wrapped as a create error.
func TestCreateOrderNetworkError(t *testing.T) {
	e := newConfigured("http://127.0.0.1:1")
	_, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "o1", AmountCents: 100})
	if err == nil || !strings.Contains(err.Error(), "epusdt create") {
		t.Fatalf("err = %v; want wrapped create error", err)
	}
}

// TestCreateOrderBadJSON covers a non-JSON gateway reply: errors with a non-JSON response message.
func TestCreateOrderBadJSON(t *testing.T) {
	srv, _ := newTestGateway(t, http.StatusOK, "<html>not json</html>")
	e := newConfigured(srv.URL)
	_, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "o1", AmountCents: 100})
	if err == nil || !strings.Contains(err.Error(), "响应非 JSON") {
		t.Fatalf("err = %v; want non-JSON error", err)
	}
}

// TestCreateOrderTruncatedBody covers the declared response length not matching the actual body: a short read errors.
func TestCreateOrderTruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("{}")) // disconnect after only part of it
	}))
	defer srv.Close()
	e := newConfigured(srv.URL)
	if _, err := e.CreateOrder(context.Background(), payment.Order{OrderNo: "o1", AmountCents: 100}); err == nil {
		t.Fatal("truncated body must error")
	}
}

// TestVerifyConfiguredCheck covers an instance without configured credentials rejecting callbacks outright.
func TestVerifyConfiguredCheck(t *testing.T) {
	if _, err := (&Epusdt{}).Verify(context.Background(), []byte("order_id=o1")); err == nil {
		t.Fatal("unconfigured gateway must reject callbacks")
	}
}

// TestVerifyBadForm covers a callback body that is not a form: errors with a bad-form message.
func TestVerifyBadForm(t *testing.T) {
	e := newConfigured("http://127.0.0.1:1")
	_, err := e.Verify(context.Background(), []byte("%%%%not-a-form%%%%"))
	if err == nil || !strings.Contains(err.Error(), "回调非表单") {
		t.Fatalf("err = %v; want bad-form error", err)
	}
}
