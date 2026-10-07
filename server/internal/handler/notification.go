package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/ringlog"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 站内信通知中心（NT-1）：四类通知逐用户落行（小用户量扇出成本可忽略），
// 已读态挂在行上。面板侧读与已读流转；dash 侧公告扇出与全量列表。
// 到期/流量预警的自动触发在 NT-2，届时直接落 storage.Notification 行即可。

const (
	NotifAnnouncement = "announcement" // 公告（dash 扇出）
	NotifExpiry       = "expiry"       // 到期提醒（NT-2 定时扫描）
	NotifTraffic      = "traffic"      // 流量预警（NT-2 定时扫描）
	NotifSystem       = "system"       // 系统事件
)

// fanoutAnnouncement 公告扇出：给全部启用用户各落一行，分批写入。
// 返回实际落行数（禁用用户不收）。
func fanoutAnnouncement(db *gorm.DB, title, body string) (int64, error) {
	var ids []int64
	if err := db.Model(&storage.User{}).Where("enabled = ?", true).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	rows := make([]storage.Notification, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, storage.Notification{UserID: id, Type: NotifAnnouncement, Title: title, Body: body})
	}
	res := db.CreateInBatches(rows, 500)
	return res.RowsAffected, res.Error
}

// createAnnouncement 发布公告（POST /api/notifications/announcement，OD 无关，NT-1）：
// 标题必填，正文选填；扇出给全部启用用户。
func (h *Handler) createAnnouncement(c *gin.Context) {
	var body struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	if body.Title == "" {
		fail(c, http.StatusBadRequest, errors.New("标题不能为空"))
		return
	}
	if len(body.Title) > 128 {
		fail(c, http.StatusBadRequest, errors.New("标题过长（≤128）"))
		return
	}
	if len(body.Body) > 512 {
		fail(c, http.StatusBadRequest, errors.New("正文过长（≤512）"))
		return
	}
	n, err := fanoutAnnouncement(h.db, body.Title, strings.TrimSpace(body.Body))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	ringlog.Default().Add(fmt.Sprintf("announcement: %s（扇出 %d 用户）", body.Title, n))
	c.JSON(http.StatusOK, gin.H{"created": n})
}

// listNotifications 全量通知列表（GET /api/notifications?limit=&type=&user_id=，dash 排障用）。
func (h *Handler) listNotifications(c *gin.Context) {
	limit := 200
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-500"))
			return
		}
		limit = n
	}
	q := h.db.Model(&storage.Notification{})
	if v := c.Query("type"); v != "" {
		q = q.Where("type = ?", v)
	}
	if v := c.Query("user_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			q = q.Where("user_id = ?", id)
		}
	}
	var rows []storage.Notification
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// deleteNotification 删除一条通知（DELETE /api/notifications/:id）。
func (h *Handler) deleteNotification(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	res := h.db.Delete(&storage.Notification{}, id)
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// panelNotifications 当前用户的通知列表（GET /api/panel/notifications?limit=&unread=1）。
func (h *Handler) panelNotifications(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-200"))
			return
		}
		limit = n
	}
	q := h.db.Where("user_id = ?", u.ID)
	if c.Query("unread") == "1" {
		q = q.Where("read_at IS NULL")
	}
	var rows []storage.Notification
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// panelUnreadCount 当前用户未读数（GET /api/panel/notifications/unread-count）。
func (h *Handler) panelUnreadCount(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	var count int64
	if err := h.db.Model(&storage.Notification{}).
		Where("user_id = ? AND read_at IS NULL", u.ID).Count(&count).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

// panelMarkRead 标记单条已读（POST /api/panel/notifications/:id/read）：
// 他人通知 404 防越权；已读重复标记幂等 200。
func (h *Handler) panelMarkRead(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var row storage.Notification
	if err := h.db.Where("id = ? AND user_id = ?", id, u.ID).First(&row).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if row.ReadAt == nil {
		now := time.Now()
		if err := h.db.Model(&row).Update("read_at", now).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// panelMarkAllRead 全部已读（POST /api/panel/notifications/read-all）。
func (h *Handler) panelMarkAllRead(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	res := h.db.Model(&storage.Notification{}).
		Where("user_id = ? AND read_at IS NULL", u.ID).
		Update("read_at", time.Now())
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": res.RowsAffected})
}
