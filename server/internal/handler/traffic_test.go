package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestTrafficRecordAndRead(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// 两个用户 + 一个节点
	for _, name := range []string{"alice", "bob"} {
		if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": name}); rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d", name, rec.Code)
		}
	}
	if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "hk", "address": "a", "port": 443, "protocol": "vless", "config": `{"uuid":"u-1"}`,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d", rec.Code)
	}

	// 批量记账：alice 三条（跨两天、含节点归属与显式时间），bob 一条
	day1 := "2026-10-01T10:00:00Z"
	day2 := "2026-10-02T10:00:00Z"
	rec := doJSON(t, r, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{
			{"user_id": 1, "node_id": 1, "rx_bytes": 100, "tx_bytes": 10, "recorded_at": day1},
			{"user_id": 1, "node_id": 1, "rx_bytes": 50, "tx_bytes": 5, "recorded_at": day1},
			{"user_id": 1, "rx_bytes": 7, "tx_bytes": 3, "recorded_at": day2},
			{"user_id": 2, "rx_bytes": 1, "tx_bytes": 2},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("record: %d %s", rec.Code, rec.Body)
	}
	if got := rec.Body.String(); got != `{"recorded":4}` {
		t.Fatalf("record body = %s", got)
	}

	// 单条写入（缺省时间）
	rec = doJSON(t, r, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{{"user_id": 2, "rx_bytes": 4, "tx_bytes": 8}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("record single: %d", rec.Code)
	}

	// alice 汇总 + 按日明细
	rec = doJSON(t, r, "GET", "/api/users/1/traffic", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read summary: %d", rec.Code)
	}
	var s struct {
		UserID     uint  `json:"user_id"`
		TotalRx    int64 `json:"total_rx"`
		TotalTx    int64 `json:"total_tx"`
		QuotaBytes int64 `json:"quota_bytes"`
		Days       []struct {
			Date string `json:"date"`
			Rx   int64  `json:"rx"`
			Tx   int64  `json:"tx"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if s.UserID != 1 || s.TotalRx != 157 || s.TotalTx != 18 {
		t.Fatalf("summary mismatch: %+v", s)
	}
	if len(s.Days) != 2 || s.Days[0].Date != "2026-10-01" || s.Days[0].Rx != 150 || s.Days[1].Date != "2026-10-02" || s.Days[1].Tx != 3 {
		t.Fatalf("days mismatch: %+v", s.Days)
	}

	// 无记录用户：零值 + 空明细
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "carol"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create carol: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/users/3/traffic", nil); rec.Code != http.StatusOK {
		t.Fatalf("empty summary: %d", rec.Code)
	}

	// 校验：空 items / 负字节 / 未知用户 / 未知节点
	if rec := doJSON(t, r, "POST", "/api/traffic-logs", map[string]any{"items": []any{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty items: %d", rec.Code)
	}
	rec = doJSON(t, r, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{{"user_id": 1, "rx_bytes": -1}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative bytes: %d", rec.Code)
	}
	rec = doJSON(t, r, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{{"user_id": 99, "rx_bytes": 1}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown user: %d", rec.Code)
	}
	rec = doJSON(t, r, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{{"user_id": 1, "node_id": 99, "rx_bytes": 1}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown node: %d", rec.Code)
	}
	_ = time.Now
}
