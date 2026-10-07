package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/ringlog"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 站内信通知中心（NT-1/NT-2）：四类通知逐用户落行（小用户量扇出成本可忽略），
// 已读态挂在行上。面板侧读与已读流转、偏好设置；dash 侧公告扇出与全量列表。
// 到期/流量预警的定时扫描在 internal/notifyscan；发放到账的事件触发在 applyGrant。

// fanoutAnnouncement 公告扇出：给全部启用用户各落一行，分批写入；
// 站外投递走同一事件接口（HERALD-4），事件行同批落 outbox（公告为管理侧
// 主动动作，不设 dedup_key——每次发布都是有意图的一次触达）。
// 另落一行站级锚点（user_id=0，TOUCH-2）：RSS /feed.xml 的数据源。
// 返回实际落行数（禁用用户不收）。
func fanoutAnnouncement(db *gorm.DB, title, body string) (int64, error) {
	var ids []int64
	if err := db.Model(&storage.User{}).Where("enabled = ?", true).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	// 锚点行先行落库（RSS 数据源）：公告是站级的，没有启用用户也要可见。
	rows := []storage.Notification{{UserID: 0, Type: storage.NotifAnnouncement, Title: title, Body: body}}
	events := make([]storage.Event, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, storage.Notification{UserID: id, Type: storage.NotifAnnouncement, Title: title, Body: body})
		events = append(events, storage.Event{
			Kind: herald.KindNotice, Severity: herald.SeverityInfo,
			Title: title, Body: body,
			Target: herald.TargetUser(id), Status: herald.StatusPending,
			OccurredAt: time.Now(), CreatedAt: time.Now(),
		})
	}
	if err := db.CreateInBatches(rows, 500).Error; err != nil {
		return 0, err
	}
	if len(events) > 0 {
		if err := db.CreateInBatches(events, 500).Error; err != nil {
			return int64(len(ids)), err
		}
	}
	// 返回用户行数（锚点行不入计数）：扇出语义=触达了多少启用用户。
	return int64(len(ids)), nil
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

// ---- 通知偏好（NT-2）----

// panelNotifyPrefs 当前用户的通知偏好（GET /api/panel/notify-prefs）。
func (h *Handler) panelNotifyPrefs(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"notify_expiry":        u.NotifyExpiry,
		"notify_traffic":       u.NotifyTraffic,
		"traffic_warn_percent": effectiveWarnPercent(u.TrafficWarnPercent),
	})
}

// panelUpdateNotifyPrefs 更新通知偏好（PUT /api/panel/notify-prefs）。
func (h *Handler) panelUpdateNotifyPrefs(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	var body struct {
		NotifyExpiry       *bool `json:"notify_expiry"`
		NotifyTraffic      *bool `json:"notify_traffic"`
		TrafficWarnPercent *int  `json:"traffic_warn_percent"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	updates := map[string]any{}
	if body.NotifyExpiry != nil {
		updates["notify_expiry"] = *body.NotifyExpiry
	}
	if body.NotifyTraffic != nil {
		updates["notify_traffic"] = *body.NotifyTraffic
	}
	if body.TrafficWarnPercent != nil {
		if *body.TrafficWarnPercent < 1 || *body.TrafficWarnPercent > 100 {
			fail(c, http.StatusBadRequest, errors.New("traffic_warn_percent must be 1-100"))
			return
		}
		updates["traffic_warn_percent"] = *body.TrafficWarnPercent
	}
	if len(updates) == 0 {
		fail(c, http.StatusBadRequest, errors.New("无可更新字段"))
		return
	}
	if err := h.db.Model(&storage.User{}).Where("id = ?", u.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// effectiveWarnPercent 归一阈值：未设/越界回落默认 80（历史行与扫描共用口径）。
func effectiveWarnPercent(v int) int {
	if v <= 0 || v > 100 {
		return 80
	}
	return v
}
