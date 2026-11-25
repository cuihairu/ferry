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

// newTestGateway spins up a fake epusdt-spec gateway that records the received form and response body.
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

// newConfigured assembles a client pointing at the fake gateway.
func newConfigured(srvURL string) *Epusdt {
	return &Epusdt{
		baseURL:   srvURL,
		token:     "secret-token",
		notifyURL: "https://panel.example.com/api/pay/epusdt/notify",
		client:    &http.Client{},
	}
}

// TestSign covers the epusdt signing rules: ASCII ascending order, empty values skipped,
// token appended directly, signature itself excluded; cross-checked against a hand-computed md5.
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
	// signature is bound to the token
	e2 := newConfigured("http://pay.example.com")
	e2.token = "other"
	if e2.sign(vals) == want {
		t.Fatal("sign should depend on token")
	}
}

// TestCreateOrder covers the main ordering path: form params (including signature) sent per protocol,
// response converted to a Receipt; also covers the gateway-business-failure and unconfigured error paths.
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
	// the form received by the gateway should carry a valid signature (re-verified with the same rule)
	vals := mustParseQuery(t, string(*got))
	eRef := newConfigured(srv.URL)
	if eRef.sign(vals) != vals.Get("signature") {
		t.Fatalf("form signature invalid: %v", vals)
	}
	if vals.Get("amount") != "10.00" || vals.Get("order_id") != "o-1" {
		t.Fatalf("form params = %v", vals)
	}

	// gateway business failure → error
	srvBad, _ := newTestGateway(t, http.StatusOK, `{"statusCode":400,"message":"金额低于起付"}`)
	if _, err := newConfigured(srvBad.URL).CreateOrder(context.Background(), payment.Order{OrderNo: "o-2", AmountCents: 100}); err == nil {
		t.Fatal("want error on statusCode 400")
	}

	// unconfigured → error
	if _, err := (&Epusdt{}).CreateOrder(context.Background(), payment.Order{OrderNo: "o", AmountCents: 1}); err == nil {
		t.Fatal("want error when unconfigured")
	}
}

// TestVerify covers callback verification: a valid callback (status=2) converts to a Callback;
// tampered signature / non-success status / bad amount each error.
func TestVerify(t *testing.T) {
	e := newConfigured("http://pay.example.com")
	// construct a valid callback per the gateway spec
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

	// tampered signature
	bad := strings.Replace(raw, sig, strings.Repeat("0", 32), 1)
	if _, err := e.Verify(context.Background(), []byte(bad)); err == nil {
		t.Fatal("want error on tampered signature")
	}
	// non-success status
	notPaid := "amount=10.00&order_id=o-1&status=1&trade_id=t-9&signature=" +
		md5hex("amount=10.00&order_id=o-1&status=1&trade_id=t-9"+"secret-token")
	if _, err := e.Verify(context.Background(), []byte(notPaid)); err == nil {
		t.Fatal("want error on status=1")
	}
	// bad amount
	badAmt := "amount=x&order_id=o-1&status=2&trade_id=t-9&signature=" +
		md5hex("amount=x&order_id=o-1&status=2&trade_id=t-9"+"secret-token")
	if _, err := e.Verify(context.Background(), []byte(badAmt)); err == nil {
		t.Fatal("want error on bad amount")
	}
}

// TestYuanCentsRoundTrip covers amount conversion (two-decimal yuan ↔ cents).
func TestYuanCentsRoundTrip(t *testing.T) {
	for cents := range map[int64]bool{1: true, 99: true, 1000: true, 12345: true} {
		back, err := yuanToCents(centsToYuan(cents))
		if err != nil || back != cents {
			t.Fatalf("roundtrip %d: %d %v", cents, back, err)
		}
	}
	if v, err := yuanToCents("10.005"); err != nil || v != 1001 { // rounded to the cent
		t.Fatalf("10.005 → %d %v, want 1001", v, err)
	}
}

// ---- test helpers ----

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
