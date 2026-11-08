package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/packages/payment"
)

// boomBody 是一读即断的请求体（网关回调中途断流）。
type boomBody struct{}

func (boomBody) Read([]byte) (int, error) { return 0, errors.New("body gone") }

// TestEpusdtNotifyBodyReadError 回调体读不出来：400 且未验签未落账。
func TestEpusdtNotifyBodyReadError(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	payment.Register(stubEpusdt{})
	req := httptest.NewRequest("POST", "/api/pay/epusdt/notify", boomBody{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("body read error: %d %s; want 400", rec.Code, rec.Body)
	}
}
