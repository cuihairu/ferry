package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCostAPIDBError 覆盖成本看板取数失败：月初至今报告构建依赖设置读取，
// 注入 settings 查询失败 → 500。
func TestCostAPIDBError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:cost_setting", db, (*storage.Setting)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/cost", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("build report: %d %s", rec.Code, rec.Body)
	}
}

// TestCostThresholdGuards 覆盖阈值设置的两个校验口：坏载荷与缺 cents 均 400。
func TestCostThresholdGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doRaw(t, r, http.MethodPut, "/api/cost/threshold", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad payload: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/cost/threshold", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing cents: %d %s", rec.Code, rec.Body)
	}
}
