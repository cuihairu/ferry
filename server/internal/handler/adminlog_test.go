package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/ringlog"
)

// TestAdminLogs 覆盖运行日志查看与清除（P1-11）。
func TestAdminLogs(t *testing.T) {
	r := newTestRouter(t)
	ring := ringlog.Default()
	ring.Clear()
	t.Cleanup(ring.Clear)

	for _, line := range []string{"l1", "l2", "l3"} {
		ring.Add(line)
	}

	// 默认返回全部（3 条）
	w := doAdmin(t, r, "GET", "/admin/logs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get logs: %d %s", w.Code, w.Body)
	}
	var out struct {
		Entries []ringlog.Entry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) != 3 || out.Entries[0].Line != "l1" || out.Entries[2].Line != "l3" {
		t.Fatalf("entries mismatch: %+v", out.Entries)
	}

	// limit 截取最近 n 条
	w = doAdmin(t, r, "GET", "/admin/logs?limit=2", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) != 2 || out.Entries[0].Line != "l2" {
		t.Fatalf("limited entries mismatch: %+v", out.Entries)
	}

	// 非法 limit 400
	if w = doAdmin(t, r, "GET", "/admin/logs?limit=0", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad limit should 400: %d", w.Code)
	}

	// 清除后为空
	if w = doAdmin(t, r, "DELETE", "/admin/logs", nil); w.Code != http.StatusOK {
		t.Fatalf("clear logs: %d %s", w.Code, w.Body)
	}
	if got := ring.Entries(0); len(got) != 0 {
		t.Fatalf("after clear entries = %v", got)
	}

	// 无令牌 401
	if w := doJSON(t, r, "GET", "/admin/logs", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("without token should 401: %d", w.Code)
	}
}
