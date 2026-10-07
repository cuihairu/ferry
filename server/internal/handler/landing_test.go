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

	// 调权重
	rec = doJSON(t, r, "PUT", fmt.Sprintf("/api/landings/%d", landingRowID), map[string]any{"weight": 60})
	if rec.Code != http.StatusOK {
		t.Fatalf("weight update: %d %s", rec.Code, rec.Body)
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
