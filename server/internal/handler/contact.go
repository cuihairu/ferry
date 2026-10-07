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

// 联系信息绑定（触达批 TOUCH-1，用户触达设计 §3）：TG 或邮箱至少一个必填；
// 例行邮件可退订（域名例行），账单类涉及权益默认必收（只可改通道，无开关）。
// 投递失败的 stale 标记与失败计数由 TOUCH-5 回执侧维护，换绑即清零。

var (
	errContactRequired = errors.New("email 与 tg_chat_id 至少填一项")
	errContactEmail    = errors.New("邮箱格式不正确")
	errContactChat     = errors.New("TG chat_id 过长（≤64）")
)

// panelContact 回读绑定与偏好（GET /api/panel/contact）：未绑定为全空行
// （行不存在也返回零值结构，前端以 email/tg_chat_id 均空判未就绪）。
func (h *Handler) panelContact(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	var row storage.UserContact
	row.UserID = u.ID
	h.db.First(&row, u.ID) // 查不到保持零值，不报错
	c.JSON(http.StatusOK, gin.H{
		"email":          row.Email,
		"tg_chat_id":     row.TgChatID,
		"routine_emails": row.RoutineEmails,
		"stale":          row.Stale,
		"bound_at":       row.BoundAt,
	})
}

// panelContactPut 绑定/换绑（PUT /api/panel/contact）：{email?, tg_chat_id?,
// routine_emails?}。两项都空 400；换绑（值变化）清 stale 与失败计数。
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
		routine = true // 首绑未指定：例行邮件默认收
	}
	if newRow || row.Email != email || row.TgChatID != chat {
		// 首绑或换绑：重置绑定时间与失效标记。
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

// 投递失败换绑闭环（触达批 TOUCH-5，用户触达设计 §3）：Herald 通道分发
// 回执驱动——failed 把触达任务标失败并置用户联系信息 stale（站内提醒换绑），
// 连续失败达阈值升级一条常规事件（换通道由 Herald 分发）；sent 清零恢复。
const contactFailStreakLimit = 3

// touchResult 由 eventResult（通道分发回执）调用；非触达事件（管理告警等
// 没有 touch_jobs 行）直接返回不联动。联动失败静默——回执留痕已落，
// 不因此让回执端点报错。
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
		h.db.Model(&ct).Update("fail_streak", 0) // 升级即清零防重复刷
	}
}

// notifyContactStale 站内提醒换绑（同一用户同日至多一条，防回执刷屏）。
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

// truncateErr 回执 detail 可能很长，落 error 列前截断（列宽 255）。
func truncateErr(s string) string {
	if len(s) > 255 {
		return s[:255]
	}
	return s
}
