package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestCostAPI 覆盖成本看板（E-23）：报告结构、阈值设置与非法值拒绝。
func TestCostAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 空库报告：结构完整、汇总为零、阈值缺省。
	rec := doJSON(t, r, "GET", "/api/cost", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get cost status = %d body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Nodes []map[string]any `json:"nodes"`
		Summary struct {
			Nodes int `json:"nodes"`
		} `json:"summary"`
		ThresholdCents int64 `json:"threshold_cents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode cost resp: %v", err)
	}
	if out.Summary.Nodes != 0 || out.ThresholdCents != 5000 {
		t.Fatalf("empty report = %+v", out)
	}

	// 设置阈值并回读。
	rec = doJSON(t, r, "PUT", "/api/cost/threshold", map[string]any{"cents": 8000})
	if rec.Code != http.StatusOK {
		t.Fatalf("put threshold status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/cost", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ThresholdCents != 8000 {
		t.Fatalf("threshold after put = %d", out.ThresholdCents)
	}

	// 非法阈值拒绝。
	rec = doJSON(t, r, "PUT", "/api/cost/threshold", map[string]any{"cents": -1})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative threshold status = %d", rec.Code)
	}

	// 节点入报告：按流量节点流量花费=流量GB×单价。
	if err := db.Exec(`INSERT INTO nodes (name, address, port, protocol, token, role, direction,
		billing_type, traffic_price_cents, monthly_cost_cents, region, isp, enabled)
		VALUES ('hk-1', 'hk.example.com', 443, 'vless', 'tok-x', 'landing', 'out',
		'按流量', 100, 3000, 'hk', '测试ISP', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO node_traffic_logs (node_id, proc, rx_bytes, tx_bytes, recorded_at)
		VALUES (1, 'xray', 6000000000, 4000000000, datetime('now'))`).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/cost", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) != 1 {
		t.Fatalf("nodes = %d", len(out.Nodes))
	}
	nc := out.Nodes[0]
	if nc["traffic_cost_cents"].(float64) != 1000 {
		t.Fatalf("traffic cost = %v", nc["traffic_cost_cents"])
	}
	if nc["fixed_cost_cents"].(float64) != 3000 {
		t.Fatalf("fixed cost = %v", nc["fixed_cost_cents"])
	}
}
