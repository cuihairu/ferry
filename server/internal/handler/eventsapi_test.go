package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestEventsAPI 覆盖 outbox 管理端：列表带计数、status 过滤、死信重投与
// 非死信冲突。
func TestEventsAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 三状态各一：sent / pending / failed（死信）
	now := time.Now()
	rows := []storage.Event{
		{Kind: "region_fault", Severity: herald.SeverityCritical, Title: "华东不可达", Target: "admin", Status: herald.StatusSent, Attempts: 1, OccurredAt: now, CreatedAt: now},
		{Kind: "cert_expiring", Severity: herald.SeverityWarning, Title: "证书 7 天到期", Target: "admin", Status: herald.StatusPending, OccurredAt: now, CreatedAt: now},
		{Kind: "node_blocked", Severity: herald.SeverityCritical, Title: "节点被封", Target: "admin", Status: herald.StatusFailed, Attempts: herald.MaxAttempts, OccurredAt: now, CreatedAt: now},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	failedID := rows[2].ID

	// 全量列表 + 计数（pending 2 = pending 1 + failed 1? 否——计数按状态分列）
	rec := doJSON(t, r, "GET", "/api/events", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Rows   []storage.Event `json:"rows"`
		Counts struct {
			Pending int64 `json:"pending"`
			Sent    int64 `json:"sent"`
			Failed  int64 `json:"failed"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(out.Rows))
	}
	if out.Counts.Pending != 1 || out.Counts.Sent != 1 || out.Counts.Failed != 1 {
		t.Fatalf("counts = %+v", out.Counts)
	}

	// status 过滤
	rec = doJSON(t, r, "GET", "/api/events?status=failed", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0].Status != herald.StatusFailed {
		t.Fatalf("failed filter = %+v", out.Rows)
	}

	// limit 越界 400
	if rec := doJSON(t, r, "GET", "/api/events?limit=0", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=0: %d", rec.Code)
	}

	// 死信重投 → 回 pending 清下次投递时刻
	if rec := doJSON(t, r, "POST", "/api/events/"+strconv.FormatInt(failedID, 10)+"/retry", nil); rec.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	var got storage.Event
	if err := db.First(&got, failedID).Error; err != nil || got.Status != herald.StatusPending || got.NextAttemptAt != nil {
		t.Fatalf("after retry = %+v err=%v", got, err)
	}

	// 二次重投冲突（已是 pending）；不存在 409
	if rec := doJSON(t, r, "POST", "/api/events/"+strconv.FormatInt(failedID, 10)+"/retry", nil); rec.Code != http.StatusConflict {
		t.Fatalf("retry pending should 409: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/events/99999/retry", nil); rec.Code != http.StatusConflict {
		t.Fatalf("retry missing should 409: %d", rec.Code)
	}
}
