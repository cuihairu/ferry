package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

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
