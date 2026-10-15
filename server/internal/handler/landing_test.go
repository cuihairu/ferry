package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestLandingAssignment(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mk := func(name, role string) uint {
		rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443,
			"protocol": "vless", "config": `{"uuid":"u1"}`, "role": role,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s(%s): %d %s", name, role, rec.Code, rec.Body)
		}
		var n map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &n)
		return uint(n["id"].(float64))
	}
	entry := mk("entry-hk", "entry")
	landing := mk("landing-us", "landing")

	// 校验：入口与区域必须二选一
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"landing_node_id": landing, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("neither scope: %d", rec.Code)
	}
	// 校验：入口节点不能当落地
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": entry, "landing_node_id": landing, "direction": "out",
	}); rec.Code == http.StatusCreated {
		// entry+landing 不同节点且角色合规，允许
	} else {
		t.Fatalf("valid node-level assign: %d %s", rec.Code, rec.Body)
	}
	// 校验：入口节点自身不能被指定为落地
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": entry, "landing_node_id": entry, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("same node: %d", rec.Code)
	}
	// 校验：非法方向
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"region": "香港", "landing_node_id": landing, "direction": "sideways",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad direction: %d", rec.Code)
	}

	// 区域级分配（region 挂、entry 空）
	rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"region": "香港", "landing_node_id": landing, "direction": "in", "weight": 30, "reason": "手动兜底",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("region-level assign: %d %s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created["strategy"] != "manual" || created["region"] != "香港" {
		t.Fatalf("created = %v", created)
	}
	landingRowID := int(created["id"].(float64))

	// 默认列表只出未释放的（两条都在）
	rec = doJSON(t, r, "GET", "/api/landings", nil)
	var rows []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 2 {
		t.Fatalf("active rows = %d, want 2", len(rows))
	}

	// direction 过滤：只出 in 向那条
	rec = doJSON(t, r, "GET", "/api/landings?direction=in", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 1 || rows[0]["direction"] != "in" {
		t.Fatalf("direction filter = %+v", rows)
	}

	// 建：坏载荷 400
	if rec := doRaw(t, r, "POST", "/api/landings", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
	// 建：权重越界 400
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"region": "hk", "landing_node_id": landing, "direction": "out", "weight": 1001,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("weight range: %d", rec.Code)
	}
	// 建：落地节点不存在 400
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"region": "hk", "landing_node_id": 99999, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing landing node: %d", rec.Code)
	}
	// 建：入口节点不存在 400
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": 99999, "landing_node_id": landing, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing entry node: %d", rec.Code)
	}
	// 建：入口角色不合规 400（landing 角色节点不能当入口）
	landingOnly := mk("landing-jp", "landing")
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": landingOnly, "landing_node_id": landing, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("entry role: %d", rec.Code)
	}
	// 建：同节点既作入口又作落地 400（both 角色两关都过，靠 ID 相等拦下）
	both := mk("both-sg", "both")
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": both, "landing_node_id": both, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("same node both roles: %d", rec.Code)
	}

	// 调权重
	rec = doJSON(t, r, "PUT", fmt.Sprintf("/api/landings/%d", landingRowID), map[string]any{"weight": 60})
	if rec.Code != http.StatusOK {
		t.Fatalf("weight update: %d %s", rec.Code, rec.Body)
	}

	// 改的负路径：id 非法/行不存在 404、坏载荷 400、权重缺失/越界 400
	if rec := doJSON(t, r, "PUT", "/api/landings/abc", map[string]any{"weight": 1}); rec.Code != http.StatusNotFound {
		t.Fatalf("update bad id: %d", rec.Code)
	}
	if rec := doJSON(t, r, "PUT", "/api/landings/99999", map[string]any{"weight": 1}); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing row: %d", rec.Code)
	}
	if rec := doRaw(t, r, "PUT", fmt.Sprintf("/api/landings/%d", landingRowID), "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("update bad json: %d", rec.Code)
	}
	if rec := doJSON(t, r, "PUT", fmt.Sprintf("/api/landings/%d", landingRowID), map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("weight required: %d", rec.Code)
	}
	if rec := doJSON(t, r, "PUT", fmt.Sprintf("/api/landings/%d", landingRowID), map[string]any{"weight": 2000}); rec.Code != http.StatusBadRequest {
		t.Fatalf("update weight range: %d", rec.Code)
	}
	// 释放：id 非法 404
	if rec := doJSON(t, r, "DELETE", "/api/landings/abc", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("release bad id: %d", rec.Code)
	}

	// 释放：置 released_at 留痕；再释放 409；默认列表不再出现
	rec = doJSON(t, r, "DELETE", fmt.Sprintf("/api/landings/%d", landingRowID), map[string]any{"reason": "换线"})
	if rec.Code != http.StatusOK {
		t.Fatalf("release: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "DELETE", fmt.Sprintf("/api/landings/%d", landingRowID), nil); rec.Code != http.StatusConflict {
		t.Fatalf("re-release: %d", rec.Code)
	}
	rec = doJSON(t, r, "GET", "/api/landings", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 1 {
		t.Fatalf("after release active rows = %d, want 1", len(rows))
	}
	// scope=all 带出留痕行
	rec = doJSON(t, r, "GET", "/api/landings?scope=all", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 2 {
		t.Fatalf("all rows = %d, want 2", len(rows))
	}

	_ = db
}
