package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestNodeConfigGuards 覆盖配置下发与节点流量/配置史的守卫分支：
// 坏 id 404、坏载荷 400、缺必填 400、配置史合法 limit 生效。
func TestNodeConfigGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "POST", "/api/nodes/abc/config", map[string]any{"proc": "xray"}); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}
	if rec := doRaw(t, r, "POST", "/api/nodes/1/config", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/nodes/1/config", map[string]any{"proc": ""}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing fields: %d", rec.Code)
	}

	// 配置史合法 limit 生效（默认 50 外的显式值路径）。
	r2, db2 := newTestRouterWithDB(t)
	nid, _ := createNodeFor(t, r2, "cfg-lim")
	for i := 0; i < 3; i++ {
		if err := db2.Create(&storage.NodeConfig{NodeID: nid, Proc: "xray",
			Version: "v", Sha256: "s", Payload: "p", Status: "applied"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if rec := doJSON(t, r2, "GET", "/api/nodes/"+itoa64(int(nid))+"/configs?limit=2", nil); rec.Code != http.StatusOK {
		t.Fatalf("configs: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r2, "GET", "/api/nodes/abc/configs", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("configs bad id: %d", rec.Code)
	}
}

// TestNodeTrafficLogsErrors 覆盖节点流量记账守卫与取数失败：坏 id 404、
// 非法 limit 400、取数失败 500；配置史取数失败 500。
func TestNodeTrafficLogsErrors(t *testing.T) {
	// 坏 id → 404。
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "GET", "/api/nodes/abc/traffic-logs", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}
	// 非法 limit → 400。
	if rec := doJSON(t, r, "GET", "/api/nodes/1/traffic-logs?limit=0", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: %d", rec.Code)
	}

	// 流量记账取数失败 → 500。
	r2, db2 := newTestRouterWithDB(t)
	regQueryFail("t:ntl", db2, (*[]storage.NodeTrafficLog)(nil))
	if rec := doJSON(t, r2, "GET", "/api/nodes/1/traffic-logs", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("traffic rows: %d %s", rec.Code, rec.Body)
	}

	// 配置史取数失败 → 500。
	r3, db3 := newTestRouterWithDB(t)
	regQueryFail("t:ncf", db3, (*[]storage.NodeConfig)(nil))
	if rec := doJSON(t, r3, "GET", "/api/nodes/1/configs", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("config rows: %d %s", rec.Code, rec.Body)
	}
}
