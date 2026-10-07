package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPanelContact 覆盖联系绑定（TOUCH-1）：至少一项必填、回读、换绑清
// 失效标记、例行邮件退订开关；无凭据 404。
func TestPanelContact(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	token := panelToken(t, r, "contact-user")

	// 未绑定：回读全空行（零值结构，前端以此判未就绪）
	rec := doPanel(t, r, token, "GET", "/api/panel/contact", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get contact: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Email         string `json:"email"`
		TgChatID      string `json:"tg_chat_id"`
		RoutineEmails bool   `json:"routine_emails"`
		Stale         bool   `json:"stale"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Email != "" || out.TgChatID != "" || out.RoutineEmails {
		t.Fatalf("expected empty contact, got %+v", out)
	}

	// 都空 400
	if rec := doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty put: %d %s", rec.Code, rec.Body)
	}
	// 邮箱格式 400
	if rec := doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{"email": "nope"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad email: %d", rec.Code)
	}

	// 只绑 TG：首绑 routine_emails 缺省 true
	rec = doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{"tg_chat_id": " 10086 "})
	if rec.Code != http.StatusOK {
		t.Fatalf("bind tg: %d %s", rec.Code, rec.Body)
	}
	var uid uint
	if err := db.Model(&storage.User{}).Where("username = ?", "contact-user").Pluck("id", &uid).Error; err != nil || uid == 0 {
		t.Fatalf("find user: %v %d", err, uid)
	}
	var row storage.UserContact
	if err := db.First(&row, uid).Error; err != nil {
		t.Fatalf("load contact: %v", err)
	}
	if row.TgChatID != "10086" || !row.RoutineEmails || row.BoundAt.IsZero() {
		t.Fatalf("unexpected contact: %+v", row)
	}

	// 换绑 email：stale 与失败计数清零，BoundAt 刷新
	db.Model(&row).Updates(map[string]any{"stale": true, "fail_streak": 2})
	oldBound := row.BoundAt
	if rec := doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{"email": "a@b.c"}); rec.Code != http.StatusOK {
		t.Fatalf("rebind: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&row, uid).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.Email != "a@b.c" || row.Stale || row.FailStreak != 0 || !row.BoundAt.After(oldBound) {
		t.Fatalf("rebind not reset: %+v", row)
	}

	// 例行邮件退订开关：只改偏好不算换绑（BoundAt 不动）
	bound := row.BoundAt
	if rec := doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{"email": "a@b.c", "routine_emails": false}); rec.Code != http.StatusOK {
		t.Fatalf("unsubscribe: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&row, uid).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.RoutineEmails || !row.BoundAt.Equal(bound) {
		t.Fatalf("pref change leaked as rebind: %+v", row)
	}

	// 回读与偏好一致
	rec = doPanel(t, r, token, "GET", "/api/panel/contact", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Email != "a@b.c" || out.RoutineEmails {
		t.Fatalf("readback mismatch: %+v", out)
	}

	// 无凭据 404
	if rec := doPanel(t, r, "", "GET", "/api/panel/contact", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
}

// TestContactStaleLoop 覆盖投递失败换绑闭环（TOUCH-5）：触达事件回执 failed
// 标任务失败+联系信息 stale+站内提醒（同日一条），三连失败升级 contact 事件
// 并清零计数；sent 回执清零恢复；非触达事件回执不联动。
func TestContactStaleLoop(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	panelToken(t, r, "stale-user")
	var uid uint
	if err := db.Model(&storage.User{}).Where("username = ?", "stale-user").Pluck("id", &uid).Error; err != nil || uid == 0 {
		t.Fatalf("find user: %v %d", err, uid)
	}
	if err := db.Create(&storage.UserContact{
		UserID: uid, Email: "dead@x.c", RoutineEmails: true, BoundAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	// 一单已投出的触达任务（模拟 TOUCH-4 投出态）
	job := storage.TouchJob{
		UserID: int64(uid), Channel: "email", Kind: "bill",
		Payload: `{"title":"t","body":"b"}`, Status: "sent", EventID: 5001, CreatedAt: time.Now(),
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	// 回执端点先校验事件行存在，补 outbox 行
	if err := db.Create(&storage.Event{ID: 5001, Kind: "bill", Target: herald.TargetUser(int64(uid)),
		Status: "sent", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	fail := func(eventID int64) {
		rec := doJSON(t, r, "POST", "/api/internal/event-results", map[string]any{
			"event_id": eventID, "channel": "email", "status": "failed", "detail": "smtp bounce",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("result: %d %s", rec.Code, rec.Body)
		}
	}
	fail(5001)

	var ct storage.UserContact
	if err := db.First(&ct, uid).Error; err != nil {
		t.Fatal(err)
	}
	if !ct.Stale || ct.FailStreak != 1 {
		t.Fatalf("stale not marked: %+v", ct)
	}
	var job2 storage.TouchJob
	if err := db.First(&job2, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if job2.Status != "failed" || job2.Error != "smtp bounce" {
		t.Fatalf("job not failed: %+v", job2)
	}
	var notices int64
	db.Model(&storage.Notification{}).Where("user_id = ? AND title = ?", uid, "联系方式已失效").Count(&notices)
	if notices != 1 {
		t.Fatalf("stale notice = %d, want 1", notices)
	}
	// 同日重复失败不再刷站内信
	fail(5001)
	db.Model(&storage.Notification{}).Where("user_id = ? AND title = ?", uid, "联系方式已失效").Count(&notices)
	if notices != 1 {
		t.Fatalf("stale notice re-sent same day: %d", notices)
	}

	// 第三次失败达阈值：升级 contact 事件 + 计数清零
	fail(5001)
	var ct2 storage.UserContact
	if err := db.First(&ct2, uid).Error; err != nil {
		t.Fatal(err)
	}
	if ct2.FailStreak != 0 || !ct2.Stale {
		t.Fatalf("escalation not reset: %+v", ct2)
	}
	var escalation int64
	db.Model(&storage.Event{}).Where("kind = ? AND target = ?", "contact", herald.TargetUser(int64(uid))).Count(&escalation)
	if escalation != 1 {
		t.Fatalf("escalation events = %d, want 1", escalation)
	}

	// sent 回执恢复：stale 清零
	rec := doJSON(t, r, "POST", "/api/internal/event-results", map[string]any{
		"event_id": 5001, "channel": "email", "status": "sent", "detail": "",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("sent result: %d", rec.Code)
	}
	var ct3 storage.UserContact
	if err := db.First(&ct3, uid).Error; err != nil {
		t.Fatal(err)
	}
	if ct3.Stale || ct3.FailStreak != 0 {
		t.Fatalf("sent not recovered: %+v", ct3)
	}
	var job3 storage.TouchJob
	db.First(&job3, job.ID)
	if job3.Status != "sent" || job3.Error != "" {
		t.Fatalf("job not recovered: %+v", job3)
	}

	// 非触达事件（无 touch_jobs 行）回执只留痕不联动
	if err := db.Create(&storage.Event{ID: 9999, Kind: "region_fault", Target: "admin",
		Status: "sent", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "POST", "/api/internal/event-results", map[string]any{
		"event_id": 9999, "channel": "email", "status": "failed", "detail": "x",
	}); rec.Code != http.StatusOK {
		t.Fatalf("non-touch result: %d", rec.Code)
	}
	var after int64
	db.Model(&storage.UserContact{}).Where("user_id = ? AND stale = ?", uid, true).Count(&after)
	if after != 0 {
		t.Fatalf("non-touch leaked stale: %d", after)
	}
}
