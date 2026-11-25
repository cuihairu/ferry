package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/packages/payment"
)

// boomBody is a request body that fails on the first read (a gateway callback that drops mid-stream).
type boomBody struct{}

func (boomBody) Read([]byte) (int, error) { return 0, errors.New("body gone") }

// TestEpusdtNotifyBodyReadError the callback body cannot be read: 400, with no signature check and no ledger entry.
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
