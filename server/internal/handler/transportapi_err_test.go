package handler

import (
	"net/http"
	"testing"
)

// TestTransportStatusWindowOK 覆盖合法判定窗口生效（30-3600 秒内取数成功）。
func TestTransportStatusWindowOK(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodGet, "/api/transport-status?window_sec=120", nil); rec.Code != http.StatusOK {
		t.Fatalf("window_sec=120: %d %s", rec.Code, rec.Body)
	}
}

// TestTransportStatusDBError 覆盖区域判定取数失败 500。
func TestTransportStatusDBError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/transport-status?window_sec=60", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("judge fail: %d %s", rec.Code, rec.Body)
	}
}
