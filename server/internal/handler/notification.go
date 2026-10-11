package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/announce"
	"github.com/cuihairu/ferry/server/internal/ringlog"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 站内信通知中心（NT-1/NT-2）：四类通知逐用户落行（小用户量扇出成本可忽略），
// 已读态挂在行上。面板侧读与已读流转、偏好设置；dash 侧公告扇出与全量列表。
// 到期/流量预警的定时扫描在 internal/notifyscan；公告扇出/窗口/重发共用
// internal/announce（P2-5）；发放到账的事件触发在 applyGrant。

// panelVisible 是 panel 侧可见面过滤（P2-5 撤下口径）：窗口结束（ends_at
// 到点）的通知不再出现在用户列表与未读数里（行保留，dash 全量列表仍可见）。
const panelVisible = "(ends_at IS NULL OR ends_at > ?)"

// createAnnouncement 发布公告（POST /api/notifications/announcement，OD 无关，NT-1）：
// 标题必填，正文选填；starts_at/ends_at 选填（RFC3339，P2-5 窗口）——starts_at
// 在未来=定时发布（先落站级锚点行，到点由 notifyscan 扇出），立即发布则
// 扇出给全部启用用户。
func (h *Handler) createAnnouncement(c *gin.Context) {
	var body struct {
		Title    string     `json:"title"`
		Body     string     `json:"body"`
		StartsAt *time.Time `json:"starts_at"`
		EndsAt   *time.Time `json:"ends_at"`
		Audience string     `json:"audience"`
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
	if body.StartsAt != nil && body.EndsAt != nil && !body.EndsAt.After(*body.StartsAt) {
		fail(c, http.StatusBadRequest, errors.New("ends_at must be after starts_at"))
		return
	}
	audience, err := announce.NormalizeAudience(body.Audience)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	n, err := announce.Fanout(h.db, announce.Input{
		Title: body.Title, Body: strings.TrimSpace(body.Body),
		StartsAt: body.StartsAt, EndsAt: body.EndsAt, Audience: audience,
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	scheduled := body.StartsAt != nil && body.StartsAt.After(time.Now())
	if scheduled {
		ringlog.Default().Add(fmt.Sprintf("announcement: %s（定时 %s 发布）", body.Title, body.StartsAt.Format(time.RFC3339)))
	} else {
		ringlog.Default().Add(fmt.Sprintf("announcement: %s（扇出 %d 用户）", body.Title, n))
	}
	c.JSON(http.StatusOK, gin.H{"created": n, "scheduled": scheduled})
}

// listAnnouncements 公告触达列表（GET /api/notifications/announcements，P2-5 ②）：
// 锚点行 + 每条的扇出数/已读数（发 n/读 m），按 AnchorID 关联用户行统计。
func (h *Handler) listAnnouncements(c *gin.Context) {
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-200"))
			return
		}
		limit = n
	}
	var anchors []storage.Notification
	if err := h.db.Where("user_id = ? AND type = ?", 0, storage.NotifAnnouncement).
		Order("id DESC").Limit(limit).Find(&anchors).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]gin.H, 0, len(anchors))
	for _, a := range anchors {
		var sent, read int64
		if err := h.db.Model(&storage.Notification{}).
			Where("anchor_id = ? AND user_id != 0", a.ID).Count(&sent).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		if err := h.db.Model(&storage.Notification{}).
			Where("anchor_id = ? AND user_id != 0 AND read_at IS NOT NULL", a.ID).Count(&read).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		out = append(out, gin.H{
			"id": a.ID, "title": a.Title, "body": a.Body,
			"starts_at": a.StartsAt, "ends_at": a.EndsAt,
			"fanout_at": a.FanoutAt, "resend_at": a.ResendAt,
			"audience":   a.Audience,
			"created_at": a.CreatedAt,
			"sent":       sent, "read": read,
		})
	}
	c.JSON(http.StatusOK, out)
}

// resendAnnouncement 外通道重发（POST /api/notifications/announcements/:id/resend，
// P2-5 ③）：对已发布公告补一轮 notice 事件（Herald 分发 TG/邮件），不重落
// 站内信行；限频 6h/公告（429），窗口已结束不重发（400）。
func (h *Handler) resendAnnouncement(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	n, err := announce.Resend(h.db, id, time.Now())
	switch {
	case errors.Is(err, announce.ErrCooldown):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "resend cooldown (6h per announcement)"})
		return
	case errors.Is(err, announce.ErrWindowClosed):
		c.JSON(http.StatusBadRequest, gin.H{"error": "announcement window closed"})
		return
	case err != nil:
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"emitted": n})
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
// deleteNotification 删通知（DELETE /api/notifications/:id）：普通行删单行；
// 锚点行（公告，user_id=0）级联撤回——扇出的用户行一并删除（公告从各用户
// 列表与未读数消失），同事务保证不留半撤状态；ends_at 定时撤下只是不可见，
// 此处是立即移除。
func (h *Handler) deleteNotification(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var target storage.Notification
	if err := h.db.Select("id", "user_id").First(&target, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&storage.Notification{}, id).Error; err != nil {
			return err
		}
		if target.UserID == 0 {
			return tx.Where("anchor_id = ?", id).Delete(&storage.Notification{}).Error
		}
		return nil
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
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
	q := h.db.Where("user_id = ? AND "+panelVisible, u.ID, time.Now())
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
		Where("user_id = ? AND read_at IS NULL AND "+panelVisible, u.ID, time.Now()).
		Count(&count).Error; err != nil {
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
		Where("user_id = ? AND read_at IS NULL AND "+panelVisible, u.ID, time.Now()).
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
