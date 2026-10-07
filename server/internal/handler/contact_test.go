package handler

import (
	"encoding/json"
	"net/http"
	"testing"

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
