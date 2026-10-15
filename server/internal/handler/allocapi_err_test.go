package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestAllocDBErrors 覆盖分配面取数失败：策略读取失败与生效中 auto 行
// 列表取数失败均 500。
func TestAllocDBErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:alloc_setting", db, (*storage.Setting)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/alloc", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("load policy: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	regQueryFail("t:alloc_rows", db, (*[]storage.LandingAssignment)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/alloc", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list rows: %d %s", rec.Code, rec.Body)
	}
}

// TestAllocPolicyBadPayload 覆盖 PUT 策略的坏载荷 400（落库前拦截）。
func TestAllocPolicyBadPayload(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doRaw(t, r, http.MethodPut, "/api/alloc/policy", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad payload: %d %s", rec.Code, rec.Body)
	}
}
