package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestTrafficRecordAndRead(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// Two users + one node
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

	// Batch accounting: three rows for alice (spanning two days, with node attribution
	// and explicit times), one for bob
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

	// Single-row write (default time)
	rec = doJSON(t, r, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{{"user_id": 2, "rx_bytes": 4, "tx_bytes": 8}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("record single: %d", rec.Code)
	}

	// alice summary + per-day detail
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

	// User with no records: zero values + empty detail
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "carol"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create carol: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/users/3/traffic", nil); rec.Code != http.StatusOK {
		t.Fatalf("empty summary: %d", rec.Code)
	}

	// Validation: empty items / negative bytes / unknown user / unknown node
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
