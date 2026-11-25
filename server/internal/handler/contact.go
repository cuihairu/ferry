package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Contact info binding (touch batch TOUCH-1, user outreach design §3): at
// least one of TG or email is required; routine emails can be unsubscribed
// (domain-level routines), while billing messages touch entitlements and are
// received by default (the channel can change, but there is no switch).
// The stale flag and failure counter for failed deliveries are maintained on the
// TOUCH-5 receipt side, and are cleared on a rebind.

var (
	errContactRequired = errors.New("email 与 tg_chat_id 至少填一项")
	errContactEmail    = errors.New("邮箱格式不正确")
	errContactChat     = errors.New("TG chat_id 过长（≤64）")
)

// panelContact reads back the binding and preferences (GET /api/panel/contact):
// an unbound user gets an all-empty row (even when the row does not exist the
// zero-value struct is returned, and the front end treats empty email and
// tg_chat_id as not ready).
func (h *Handler) panelContact(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	var row storage.UserContact
	row.UserID = u.ID
	h.db.First(&row, u.ID) // keep the zero value if nothing is found, no error
	c.JSON(http.StatusOK, gin.H{
		"email":          row.Email,
		"tg_chat_id":     row.TgChatID,
		"routine_emails": row.RoutineEmails,
		"stale":          row.Stale,
		"bound_at":       row.BoundAt,
	})
}

// panelContactPut binds/rebinds (PUT /api/panel/contact): {email?, tg_chat_id?,
// routine_emails?}. Both empty is 400; a rebind (a value change) clears stale
// and the failure counter.
func (h *Handler) panelContactPut(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	var in struct {
		Email         string `json:"email"`
		TgChatID      string `json:"tg_chat_id"`
		RoutineEmails *bool  `json:"routine_emails"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	email := strings.TrimSpace(in.Email)
	chat := strings.TrimSpace(in.TgChatID)
	if email == "" && chat == "" {
		fail(c, http.StatusBadRequest, errContactRequired)
		return
	}
	if email != "" && (!strings.Contains(email, "@") || len(email) > 255) {
		fail(c, http.StatusBadRequest, errContactEmail)
		return
	}
	if len(chat) > 64 {
		fail(c, http.StatusBadRequest, errContactChat)
		return
	}

	var row storage.UserContact
	newRow := h.db.First(&row, u.ID).Error != nil
	routine := row.RoutineEmails
	if in.RoutineEmails != nil {
		routine = *in.RoutineEmails
	} else if newRow {
		routine = true // unspecified on the first bind: routine emails are received by default
	}
	if newRow || row.Email != email || row.TgChatID != chat {
		// First bind or rebind: reset the binding time and the stale flag.
		row.BoundAt = time.Now()
		row.Stale = false
		row.FailStreak = 0
	}
	row.UserID = u.ID
	row.Email = email
	row.TgChatID = chat
	row.RoutineEmails = routine
	if newRow {
		if err := h.db.Create(&row).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	} else if err := h.db.Save(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"email": row.Email, "tg_chat_id": row.TgChatID,
		"routine_emails": row.RoutineEmails, "stale": row.Stale,
	})
}

// Delivery-failure rebind loop (touch batch TOUCH-5, user outreach design §3):
// driven by Herald channel dispatch receipts — failed marks the touch job
// failed and sets the user's contact info stale (an in-app prompt to rebind),
// and consecutive failures reaching the threshold escalate into a routine event
// (channel switching is Herald's business); sent clears everything and restores.
const contactFailStreakLimit = 3

// touchResult is called by eventResult (channel dispatch receipts); non-touch
// events (admin alerts and the like, with no touch_jobs row) return
// immediately without linkage. Linkage failures are silent — the receipt trail
// is already recorded, so they must not make the receipt endpoint fail.
func (h *Handler) touchResult(eventID int64, status, detail string) {
	var job storage.TouchJob
	if err := h.db.Where("event_id = ?", eventID).First(&job).Error; err != nil {
		return
	}
	if status == "sent" {
		h.db.Model(&job).Updates(map[string]any{"status": herald.StatusSent, "error": ""})
		h.db.Model(&storage.UserContact{}).Where("user_id = ?", job.UserID).
			Updates(map[string]any{"stale": false, "fail_streak": 0})
		return
	}

	h.db.Model(&job).Updates(map[string]any{"status": herald.StatusFailed, "error": truncateErr(detail)})
	var ct storage.UserContact
	if err := h.db.First(&ct, job.UserID).Error; err != nil {
		ct = storage.UserContact{UserID: uint(job.UserID), BoundAt: time.Now()}
		if err := h.db.Create(&ct).Error; err != nil {
			return
		}
	}
	streak := ct.FailStreak + 1
	h.db.Model(&ct).Updates(map[string]any{"stale": true, "fail_streak": streak})
	h.notifyContactStale(ct)

	if streak >= contactFailStreakLimit {
		_, _ = herald.Emit(h.db, herald.EmitInput{
			Kind: herald.KindContact, Severity: herald.SeverityWarning,
			Title: "联系方式投递连续失败",
			Body: fmt.Sprintf("用户 #%d 的 %s 触达连续 %d 次投递失败，已标记失效待换绑；请用户换绑或改用其他通道触达。",
				job.UserID, job.Kind, contactFailStreakLimit),
			Target: herald.TargetUser(job.UserID),
			Meta:   map[string]any{"touch_job_id": job.ID, "kind": job.Kind, "streak": contactFailStreakLimit},
		})
		h.db.Model(&ct).Update("fail_streak", 0) // clearing on escalation prevents repeated spam
	}
}

// notifyContactStale prompts rebind in app (at most one per user per day, against receipt spam).
func (h *Handler) notifyContactStale(ct storage.UserContact) {
	dayStart := time.Now().Truncate(24 * time.Hour)
	var n int64
	h.db.Model(&storage.Notification{}).
		Where("user_id = ? AND title = ? AND created_at >= ?", ct.UserID, "联系方式已失效", dayStart).
		Count(&n)
	if n > 0 {
		return
	}
	h.db.Create(&storage.Notification{
		UserID: int64(ct.UserID), Type: storage.NotifSystem,
		Title:     "联系方式已失效",
		Body:      "邮箱或 Telegram 投递失败，通知已暂停；请到面板概览页换绑联系方式。",
		CreatedAt: time.Now(),
	})
}

// truncateErr truncates the receipt detail, which can be long, before it lands
// in the error column (column width 255); truncation backs off to a UTF-8
// character boundary (detail may contain Chinese, and a hard byte cut would
// split a character in half).
func truncateErr(s string) string {
	return truncate(s, 255)
}
