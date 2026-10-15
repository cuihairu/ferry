package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCostRefDBErrors 覆盖参考价面取数失败分支：来源读取（settings 查询）
// 失败试查与批量对账同口径 500、批量对账节点取数失败 500、导入坏载荷 400。
func TestCostRefDBErrors(t *testing.T) {
	// 试查：来源读取失败 → 500。
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:refsetting", db, (*storage.Setting)(nil))
	if rec := doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&spec=100m", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("probe source: %d %s", rec.Code, rec.Body)
	}

	// 批量对账：来源读取失败 → 500。
	r2, db2 := newTestRouterWithDB(t)
	regQueryFail("t:refsetting2", db2, (*storage.Setting)(nil))
	if rec := doJSON(t, r2, "GET", "/api/cost/ref-check", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("check source: %d %s", rec.Code, rec.Body)
	}

	// 批量对账：节点取数失败 → 500（先导入表让来源非空）。
	r3, db3 := newTestRouterWithDB(t)
	if rec := doJSON(t, r3, "PUT", "/api/cost/ref-table", map[string]any{"table": refTestTable}); rec.Code != http.StatusOK {
		t.Fatalf("put table: %d %s", rec.Code, rec.Body)
	}
	regQueryFail("t:refnodes", db3, (*[]storage.Node)(nil))
	if rec := doJSON(t, r3, "GET", "/api/cost/ref-check", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("nodes fail: %d %s", rec.Code, rec.Body)
	}

	// 导入：坏载荷 → 400。
	if rec := doRaw(t, r3, "PUT", "/api/cost/ref-table", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
}
