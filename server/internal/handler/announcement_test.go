package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/announce"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// feedText 取 /feed.xml 文本（公开端点，断言窗口过滤用）。
func feedText(t *testing.T, r *gin.Engine) string {
	t.Helper()
	rec := doJSON(t, r, "GET", "/feed.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed: %d %s", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// TestAnnouncementWindow 覆盖 P2-5 ①：定时发布（starts_at 未来只落锚点行，
// 到点由 RunDue 扇出）、撤下（ends_at 后 panel 列表与未读不再可见，RSS 不出）。
func TestAnnouncementWindow(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	token := panelToken(t, r, "win-u")

	// 定时发布：starts_at 未来 → 用户不收（created=0），RSS 不出。
	start := time.Now().Add(time.Hour).Truncate(time.Second)
	end := start.Add(2 * time.Hour)
	rec := doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{
		"title": "定时公告", "body": "到点可见",
		"starts_at": start.Format(time.RFC3339), "ends_at": end.Format(time.RFC3339),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("scheduled announcement: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Created   int64 `json:"created"`
		Scheduled bool  `json:"scheduled"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Created != 0 || !out.Scheduled {
		t.Fatalf("scheduled resp = %+v, want created=0 scheduled=true", out)
	}
	if strings.Contains(feedText(t, r), "定时公告") {
		t.Fatal("future announcement leaked into feed")
	}
	var userRows int64
	if err := db.Model(&storage.Notification{}).Where("user_id != 0 AND type = ?", storage.NotifAnnouncement).
		Count(&userRows).Error; err != nil {
		t.Fatal(err)
	}
	if userRows != 0 {
		t.Fatalf("user rows before due = %d, want 0", userRows)
	}

	// starts_at > ends_at 校验 400。
	if rec := doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{
		"title": "坏窗口", "starts_at": end.Format(time.RFC3339), "ends_at": start.Format(time.RFC3339),
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window: %d", rec.Code)
	}

	// 到点扇出（notifyscan Sweep 内的 announce.RunDue）。
	n, err := announce.RunDue(db, start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("RunDue fanned = %d, want 1", n)
	}
	var rows []storage.Notification
	if err := db.Where("user_id != 0 AND type = ?", storage.NotifAnnouncement).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].AnchorID == 0 || rows[0].EndsAt == nil {
		t.Fatalf("user rows after due = %+v", rows)
	}
	// 锚点行 fanout_at 回写、重复 RunDue 不再扇。
	var anchor storage.Notification
	if err := db.Where("user_id = ? AND type = ?", 0, storage.NotifAnnouncement).First(&anchor).Error; err != nil {
		t.Fatal(err)
	}
	if anchor.FanoutAt == nil {
		t.Fatal("anchor fanout_at not set")
	}
	if n, _ := announce.RunDue(db, start.Add(2*time.Minute)); n != 0 {
		t.Fatalf("second RunDue fanned = %d, want 0", n)
	}
	// 窗口内：panel 可见、未读 1、RSS 出。
	rec = doPanel(t, r, token, "GET", "/api/panel/notifications", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 1 {
		t.Fatalf("panel list in window = %d rows, want 1", len(rows))
	}
	if !strings.Contains(feedText(t, r), "定时公告") {
		t.Fatal("in-window announcement missing from feed")
	}

	// 撤下：ends_at 过去 → panel 列表与未读不再可见，RSS 不出；行保留。
	if err := db.Model(&storage.Notification{}).Where("id IN (?)",
		db.Model(&storage.Notification{}).Select("id").Where("type = ? AND title = ?", storage.NotifAnnouncement, "定时公告"),
	).Update("ends_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	rec = doPanel(t, r, token, "GET", "/api/panel/notifications", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 0 {
		t.Fatalf("panel list after takedown = %d rows, want 0", len(rows))
	}
	rec = doPanel(t, r, token, "GET", "/api/panel/notifications/unread-count", nil)
	var cnt struct {
		Count int64 `json:"count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cnt)
	if cnt.Count != 0 {
		t.Fatalf("unread after takedown = %d, want 0", cnt.Count)
	}
	if strings.Contains(feedText(t, r), "定时公告") {
		t.Fatal("taken-down announcement still in feed")
	}
	var all int64
	if err := db.Model(&storage.Notification{}).Where("type = ?", storage.NotifAnnouncement).Count(&all).Error; err != nil {
		t.Fatal(err)
	}
	if all != 2 { // 锚点行+用户行都在，只是不可见
		t.Fatalf("rows after takedown = %d, want 2", all)
	}
}

// TestAnnouncementStatsResend 覆盖 P2-5 ②③：发 n/读 m 统计回流、外通道
// 重发位与 6h 限频、窗口关闭不重发。
func TestAnnouncementStatsResend(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	tokA := panelToken(t, r, "stat-a")
	panelToken(t, r, "stat-b")

	rec := doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{"title": "触达公告"})
	if rec.Code != http.StatusOK {
		t.Fatalf("announcement: %d %s", rec.Code, rec.Body)
	}
	// 统计：发 2 读 0。
	type stat struct {
		ID   int64  `json:"id"`
		Sent int64  `json:"sent"`
		Read int64  `json:"read"`
		Name string `json:"title"`
	}
	getStats := func() []stat {
		rec := doJSON(t, r, "GET", "/api/notifications/announcements", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("stats: %d %s", rec.Code, rec.Body)
		}
		var rows []stat
		_ = json.Unmarshal(rec.Body.Bytes(), &rows)
		return rows
	}
	stats := getStats()
	if len(stats) != 1 || stats[0].Sent != 2 || stats[0].Read != 0 {
		t.Fatalf("stats = %+v, want 1 row sent=2 read=0", stats)
	}
	// 一人已读 → 读 1。
	rec = doPanel(t, r, tokA, "GET", "/api/panel/notifications", nil)
	var rows []storage.Notification
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	rec = doPanel(t, r, tokA, "POST", "/api/panel/notifications/"+strconv.FormatInt(rows[0].ID, 10)+"/read", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read: %d %s", rec.Code, rec.Body)
	}
	stats = getStats()
	if stats[0].Read != 1 {
		t.Fatalf("read = %d, want 1", stats[0].Read)
	}

	// 重发：一轮 notice 事件外发（启用用户各一条，站内信不重落）。
	var before int64
	db.Model(&storage.Event{}).Where("kind = ?", "notice").Count(&before)
	anchorID := stats[0].ID
	rec = doJSON(t, r, "POST", "/api/notifications/announcements/"+strconv.FormatInt(anchorID, 10)+"/resend", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resend: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Emitted int64 `json:"emitted"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Emitted != 2 {
		t.Fatalf("resend emitted = %d, want 2", out.Emitted)
	}
	var after int64
	db.Model(&storage.Event{}).Where("kind = ?", "notice").Count(&after)
	if after-before != 2 {
		t.Fatalf("notice events delta = %d, want 2", after-before)
	}
	var userRows int64
	db.Model(&storage.Notification{}).Where("user_id != 0 AND type = ?", storage.NotifAnnouncement).Count(&userRows)
	if userRows != 2 {
		t.Fatalf("user rows after resend = %d, want 2 (in-app rows not duplicated)", userRows)
	}

	// 限频：6h 内再发 429。
	rec = doJSON(t, r, "POST", "/api/notifications/announcements/"+strconv.FormatInt(anchorID, 10)+"/resend", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("resend again: %d, want 429", rec.Code)
	}
	// 统计行 resend_at 回写。
	stats = getStats()
	if stats[0].Sent != 2 || stats[0].Read != 1 {
		t.Fatalf("stats after resend = %+v", stats[0])
	}

	// 窗口关闭不重发（撤下语义优先）。
	if err := db.Model(&storage.Notification{}).Where("id = ?", anchorID).
		Update("ends_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "POST", "/api/notifications/announcements/"+strconv.FormatInt(anchorID, 10)+"/resend", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("resend closed: %d, want 400", rec.Code)
	}
	// 不存在的公告 404。
	rec = doJSON(t, r, "POST", "/api/notifications/announcements/999999/resend", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("resend missing: %d, want 404", rec.Code)
	}
}
