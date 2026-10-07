package handler

import (
	"encoding/json"
	"strconv"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestNotificationCenter 覆盖 NT-1：公告扇出（禁用用户不收）、未读数、
// 已读流转（单条幂等/他人 404/全部已读）、过滤与删除。
func TestNotificationCenter(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	u1 := panelToken(t, r, "notif-a")
	u2 := panelToken(t, r, "notif-b")

	// 公告扇出：两名启用用户各一行
	rec := doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{
		"title": "维护通知", "body": "周日 02:00-03:00 升级",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("announcement: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Created int64 `json:"created"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Created != 2 {
		t.Fatalf("created = %d, want 2", out.Created)
	}

	// 未读数
	getCount := func(token string) int64 {
		rec := doPanel(t, r, token, "GET", "/api/panel/notifications/unread-count", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("unread-count: %d %s", rec.Code, rec.Body)
		}
		var v struct {
			Count int64 `json:"count"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		return v.Count
	}
	if c := getCount(u1); c != 1 {
		t.Fatalf("u1 unread = %d, want 1", c)
	}
	if c := getCount(u2); c != 1 {
		t.Fatalf("u2 unread = %d, want 1", c)
	}

	// 列表与已读流转
	rec = doPanel(t, r, u1, "GET", "/api/panel/notifications", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var rows []storage.Notification
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Type != "announcement" || rows[0].ReadAt != nil {
		t.Fatalf("u1 list = %+v", rows)
	}
	mine := rows[0].ID

	// 他人通知 404 防越权
	rec = doPanel(t, r, u1, "POST", "/api/panel/notifications/999999/read", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("mark missing: %d", rec.Code)
	}

	// 单条已读幂等
	for i := 0; i < 2; i++ {
		rec = doPanel(t, r, u1, "POST", "/api/panel/notifications/"+strconv.FormatInt(mine, 10)+"/read", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("mark read: %d %s", rec.Code, rec.Body)
		}
	}
	if c := getCount(u1); c != 0 {
		t.Fatalf("u1 unread after read = %d, want 0", c)
	}

	// 系统通知直落（NT-2 触发口径）：未读恢复为 1，read-all 清零
	if err := db.Create(&storage.Notification{
		UserID: 1, Type: "system", Title: "节点恢复失败", CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if c := getCount(u1); c != 1 {
		t.Fatalf("u1 unread after seed = %d, want 1", c)
	}
	rec = doPanel(t, r, u1, "POST", "/api/panel/notifications/read-all", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("read-all: %d %s", rec.Code, rec.Body)
	}
	var ra struct {
		Updated int64 `json:"updated"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ra)
	if ra.Updated != 1 {
		t.Fatalf("read-all updated = %d, want 1", ra.Updated)
	}
	// unread=1 过滤：全读后为空
	rec = doPanel(t, r, u1, "GET", "/api/panel/notifications?unread=1", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 0 {
		t.Fatalf("unread filter = %d rows, want 0", len(rows))
	}

	// 禁用用户不收扇出
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "notif-off", "quota_bytes": 1 << 20})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create u3: %d %s", rec.Code, rec.Body)
	}
	if err := db.Model(&storage.User{}).Where("username = ?", "notif-off").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{"title": "第二轮"})
	if rec.Code != http.StatusOK {
		t.Fatalf("announcement 2: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Created != 2 {
		t.Fatalf("fanout with disabled = %d, want 2", out.Created)
	}

	// 管理端列表 type 过滤
	rec = doJSON(t, r, "GET", "/api/notifications?type=system", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list: %d", rec.Code)
	}
	var all []storage.Notification
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Type != "system" {
		t.Fatalf("type filter = %+v", all)
	}

	// 删除
	rec = doJSON(t, r, "DELETE", "/api/notifications/"+strconv.FormatInt(all[0].ID, 10), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := doJSON(t, r, "DELETE", "/api/notifications/"+strconv.FormatInt(all[0].ID, 10), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}

	// 校验：标题空 400；无凭据 404
	if rec := doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{"title": "  "}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty title: %d", rec.Code)
	}
	if rec := doPanel(t, r, "", "GET", "/api/panel/notifications", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
}
