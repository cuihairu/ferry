package handler

import (
	"errors"
	"net/http"
	"strconv"

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
