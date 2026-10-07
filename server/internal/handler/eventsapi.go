package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 事件 outbox（HERALD-1）管理端：列表与状态计数。事件由后台任务与
// HERALD-3 起的告警接入生产，这里只读——通道故障以 failed 计数标红。

// listEvents 返回事件 outbox（新在前）与各状态计数。
func (h *Handler) listEvents(c *gin.Context) {
	limit := 200
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-1000"))
			return
		}
		limit = n
	}
	q := h.db.Model(&storage.Event{}).Order("id DESC").Limit(limit)
	if v := c.Query("status"); v != "" {
		q = q.Where("status = ?", v)
	}
	if v := c.Query("kind"); v != "" {
		q = q.Where("kind = ?", v)
	}
	rows := []storage.Event{}
	if err := q.Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	pending, sent, failedCount, err := herald.Count(h.db)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"rows": rows,
		"counts": gin.H{
			"pending": pending, "sent": sent, "failed": failedCount,
		},
	})
}

// retryEvent 把一条 failed 死信复位为 pending 立即重投（通道修复后的
// 人工补投口；pending/sent 不适用）。
func (h *Handler) retryEvent(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}
	res := h.db.Model(&storage.Event{}).
		Where("id = ? AND status = ?", id, herald.StatusFailed).
		Updates(map[string]any{"status": herald.StatusPending, "next_attempt_at": nil})
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusConflict, errors.New("仅失败死信可重投"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// eventResultInput 是 Herald 异步回投的通道分发回执（《告警通道设计》§3）：
// event_id 为投递载荷里的 ferry outbox id。
type eventResultInput struct {
	EventID int64  `json:"event_id"`
	Channel string `json:"channel"`
	Status  string `json:"status"` // sent/failed
	Detail  string `json:"detail"`
}

// eventResult 落一条通道维度投递回执；事件本体状态不动（ferry→Herald 腿
// 2xx 即已置 sent，通道分发与重试由 Herald 管，回执只做留痕）。
func (h *Handler) eventResult(c *gin.Context) {
	var in eventResultInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.EventID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("event_id is required"))
		return
	}
	if in.Channel == "" {
		fail(c, http.StatusBadRequest, errors.New("channel is required"))
		return
	}
	if in.Status != "sent" && in.Status != "failed" {
		fail(c, http.StatusBadRequest, errors.New("status must be sent/failed"))
		return
	}
	var ev storage.Event
	if err := h.db.Select("id").First(&ev, in.EventID).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("event not found"))
		return
	}
	row := storage.EventDelivery{
		EventID: in.EventID, Channel: in.Channel, Status: in.Status,
		Detail: in.Detail, At: time.Now(),
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	h.touchResult(in.EventID, in.Status, in.Detail) // 触达任务联动（TOUCH-5）
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
