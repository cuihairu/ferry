package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/save"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSaveStatsAPI 覆盖节省报表回读（SAVE-7）：save_stats 按日行 +
// 折算费用（仅按流量计费节点有单价）+ 全网合计 + days 过滤。
func TestSaveStatsAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 两台节点：一台按流量计费带单价（分/GB），一台包月无边际成本
	for _, name := range []string{"metered", "flat"} {
		if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
		}); rec.Code != http.StatusCreated {
			t.Fatalf("create node %s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if err := db.Model(&storage.Node{}).Where("name=?", "metered").
		Updates(map[string]any{"billing_type": "按流量", "traffic_price_cents": 30}).Error; err != nil {
		t.Fatalf("price metered node: %v", err)
	}

	today := time.Now().UTC()
	seed := []storage.NodeTrafficLog{
		// metered 今日：直连 2GB + 拦截 0.5GB → (2.5GB × 30分) = 75 分
		{NodeID: 1, Proc: "xray", DirectBytes: 2_000_000_000, BlockedBytes: 500_000_000,
			RecordedAt: today.Add(-2 * time.Hour)},
		// flat 今日：直连 1GB，无单价不折算
		{NodeID: 2, Proc: "xray", DirectBytes: 1_000_000_000, RecordedAt: today.Add(-2 * time.Hour)},
		// metered 40 天前：days=30 过滤外
		{NodeID: 1, Proc: "xray", DirectBytes: 999, RecordedAt: today.AddDate(0, 0, -40)},
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}
	if _, err := save.Sweep(db); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	rec := doJSON(t, r, "GET", "/api/save-stats?days=30", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get save-stats: %d %s", rec.Code, rec.Body)
	}
	var res struct {
		Days  int `json:"days"`
		Rows  []struct {
			NodeID        uint   `json:"node_id"`
			Name          string `json:"name"`
			Day           string `json:"day"`
			DirectBytes   int64  `json:"direct_bytes"`
			BlockedBytes  int64  `json:"blocked_bytes"`
			CacheHitBytes int64  `json:"cache_hit_bytes"`
			CostCents     int64  `json:"cost_cents"`
		} `json:"rows"`
		Total struct {
			DirectBytes  int64 `json:"direct_bytes"`
			BlockedBytes int64 `json:"blocked_bytes"`
			CostCents    int64 `json:"cost_cents"`
		} `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Days != 30 || len(res.Rows) != 2 {
		t.Fatalf("days=%d rows=%d, want 30/2（40 天前过滤外）: %+v", res.Days, len(res.Rows), res.Rows)
	}
	// 行序 day ASC, node_id ASC：metered(1) 在前
	metered, flat := res.Rows[0], res.Rows[1]
	if metered.Name != "metered" || metered.DirectBytes != 2_000_000_000 || metered.CostCents != 75 {
		t.Fatalf("metered row = %+v, want 2GB/75 分", metered)
	}
	if flat.Name != "flat" || flat.DirectBytes != 1_000_000_000 || flat.CostCents != 0 {
		t.Fatalf("flat row = %+v, want 1GB/0 分（包月不折算）", flat)
	}
	if res.Total.DirectBytes != 3_000_000_000 || res.Total.BlockedBytes != 500_000_000 || res.Total.CostCents != 75 {
		t.Fatalf("total mismatch: %+v", res.Total)
	}

	// 非法 days 400
	if rec := doJSON(t, r, "GET", "/api/save-stats?days=0", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("days=0 should 400: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/save-stats?days=abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("days=abc should 400: %d", rec.Code)
	}
}
