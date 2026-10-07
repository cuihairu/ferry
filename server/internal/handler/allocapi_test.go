package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestAllocAPI 覆盖策略查看/设置（E-21）：默认策略、设置落库、非法档位拒绝、
// auto 行列表（manual 行不混入）。
func TestAllocAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 默认策略
	rec := doJSON(t, r, "GET", "/api/alloc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get alloc status = %d body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Policy struct {
			Out            string `json:"out"`
			In             string `json:"in"`
			RebalanceStart int    `json:"rebalance_start"`
			RebalanceEnd   int    `json:"rebalance_end"`
		} `json:"policy"`
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode alloc resp: %v", err)
	}
	if out.Policy.Out != "balanced" || out.Policy.In != "balanced" || out.Policy.RebalanceStart != 1 || out.Policy.RebalanceEnd != 7 {
		t.Fatalf("default policy = %+v", out.Policy)
	}

	// 设置档位与低峰窗口并回读
	rec = doJSON(t, r, "PUT", "/api/alloc/policy", map[string]any{
		"out": "cost_first", "in": "perf_first", "rebalance_start": 2, "rebalance_end": 6,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("put policy status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/alloc", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Policy.Out != "cost_first" || out.Policy.In != "perf_first" || out.Policy.RebalanceStart != 2 || out.Policy.RebalanceEnd != 6 {
		t.Fatalf("policy after put = %+v", out.Policy)
	}

	// 非法窗口拒绝
	rec = doJSON(t, r, "PUT", "/api/alloc/policy", map[string]any{
		"out": "balanced", "in": "balanced", "rebalance_start": 9, "rebalance_end": 9,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid window status = %d", rec.Code)
	}

	// 非法档位拒绝
	rec = doJSON(t, r, "PUT", "/api/alloc/policy", map[string]any{
		"out": "greedy", "in": "balanced",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid policy status = %d", rec.Code)
	}

	// auto 行入列表，manual 行不混入
	if err := db.Exec(`INSERT INTO landing_assignments
		(entry_node_id, landing_node_id, direction, strategy, assigned_at)
		VALUES (1, 2, 'out', 'least_conn', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO landing_assignments
		(entry_node_id, landing_node_id, direction, strategy, assigned_at)
		VALUES (1, 3, 'out', 'manual', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/alloc", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0]["strategy"] != "least_conn" {
		t.Fatalf("rows = %+v", out.Rows)
	}
}
