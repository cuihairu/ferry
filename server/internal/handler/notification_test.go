package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
	// 站外事件（HERALD-4）：公告同批落 notice 事件，启用用户各一条
	var evs []storage.Event
	if err := db.Where("kind = ?", "notice").Find(&evs).Error; err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Kind != "notice" || evs[0].Severity != "info" || !strings.HasPrefix(evs[0].Target, "user:") {
		t.Fatalf("notice events = %+v", evs)
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
	// 禁用用户不落 notice 事件（第二轮 +2=4）
	var n int64
	db.Model(&storage.Event{}).Where("kind = ?", "notice").Count(&n)
	if n != 4 {
		t.Fatalf("notice events after round2 = %d, want 4", n)
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

// TestNotifyPrefs 覆盖 NT-2 偏好：默认值读取、部分更新、阈值校验。
func TestNotifyPrefs(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	token := panelToken(t, r, "pref-u")

	// 默认：开/开/80
	rec := doPanel(t, r, token, "GET", "/api/panel/notify-prefs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get prefs: %d %s", rec.Code, rec.Body)
	}
	var prefs struct {
		NotifyExpiry  bool `json:"notify_expiry"`
		NotifyTraffic bool `json:"notify_traffic"`
		WarnPercent   int  `json:"traffic_warn_percent"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &prefs); err != nil {
		t.Fatal(err)
	}
	if !prefs.NotifyExpiry || !prefs.NotifyTraffic || prefs.WarnPercent != 80 {
		t.Fatalf("default prefs = %+v", prefs)
	}

	// 部分更新：只关到期提醒，其余不动
	rec = doPanel(t, r, token, "PUT", "/api/panel/notify-prefs", map[string]any{"notify_expiry": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("put prefs: %d %s", rec.Code, rec.Body)
	}
	rec = doPanel(t, r, token, "GET", "/api/panel/notify-prefs", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &prefs)
	if prefs.NotifyExpiry || !prefs.NotifyTraffic || prefs.WarnPercent != 80 {
		t.Fatalf("prefs after partial update = %+v", prefs)
	}

	// 阈值越界 400；合法阈值生效
	if rec := doPanel(t, r, token, "PUT", "/api/panel/notify-prefs", map[string]any{"traffic_warn_percent": 0}); rec.Code != http.StatusBadRequest {
		t.Fatalf("zero percent: %d", rec.Code)
	}
	if rec := doPanel(t, r, token, "PUT", "/api/panel/notify-prefs", map[string]any{"traffic_warn_percent": 101}); rec.Code != http.StatusBadRequest {
		t.Fatalf("101 percent: %d", rec.Code)
	}
	rec = doPanel(t, r, token, "PUT", "/api/panel/notify-prefs", map[string]any{"traffic_warn_percent": 95})
	if rec.Code != http.StatusOK {
		t.Fatalf("put 95: %d", rec.Code)
	}
	rec = doPanel(t, r, token, "GET", "/api/panel/notify-prefs", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &prefs)
	if prefs.WarnPercent != 95 {
		t.Fatalf("warn percent = %d, want 95", prefs.WarnPercent)
	}
}

// TestGrantNotifiesUser 覆盖 NT-2 事件触发：兑换发放成功即落一条 system
// 站内信（applyGrant 事务内），流量口径给人类可读量级。
func TestGrantNotifiesUser(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	token := panelToken(t, r, "grant-u")
	userID := 1

	rec := doJSON(t, r, "POST", "/api/card-batches", map[string]any{
		"name": "1GB 卡", "grant_type": "add_quota", "grant_value": 1 << 30, "total": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create batch: %d %s", rec.Code, rec.Body)
	}
	var codeRows []struct {
		Code string `json:"code"`
	}
	rec = doJSON(t, r, "GET", "/api/card-batches/1/codes", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &codeRows); err != nil {
		t.Fatal(err)
	}
	if len(codeRows) != 1 {
		t.Fatalf("codes = %+v", codeRows)
	}

	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codeRows[0].Code, "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}

	var notifs []storage.Notification
	if err := db.Where("user_id = ? AND type = ?", userID, storage.NotifSystem).Find(&notifs).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 1 {
		t.Fatalf("system notifications = %d, want 1", len(notifs))
	}
	if notifs[0].Title != "流量已到账：+1 GB" {
		t.Fatalf("title = %q", notifs[0].Title)
	}
	// 站外事件（HERALD-4）：同事务落 order 事件，target=user:<id> 带 order_no
	var evs []storage.Event
	if err := db.Where("kind = ?", "order").Find(&evs).Error; err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Target != fmt.Sprintf("user:%d", userID) || evs[0].DedupKey == "" {
		t.Fatalf("order events = %+v", evs)
	}
	if !strings.Contains(evs[0].Meta, "order_no") || !strings.Contains(evs[0].Title, "1 GB") {
		t.Fatalf("order event = %+v", evs[0])
	}
	// 未读数应计上
	rec = doPanel(t, r, token, "GET", "/api/panel/notifications/unread-count", nil)
	var cnt struct {
		Count int64 `json:"count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cnt)
	if cnt.Count != 1 {
		t.Fatalf("unread = %d, want 1", cnt.Count)
	}
}
