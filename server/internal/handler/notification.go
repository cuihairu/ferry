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

// In-app notification center (NT-1/NT-2): the four notification kinds write one
// row per user (fan-out cost is negligible at this user scale), and read state
// lives on the row. The panel side reads and drives read transitions plus
// preferences; the dash side drives announcement fan-out and the full listing.
// Scheduled expiry/traffic-warning scans live in internal/notifyscan;
// announcement fan-out/window/resend is shared via internal/announce (P2-5);
// grant-credit event triggers happen in applyGrant.

// panelVisible is the panel-side visibility filter (P2-5 teardown rule): a
// notification whose window has ended (ends_at reached) no longer appears in the
// user's list or unread count (the row is kept; the dash full listing still
// sees it).
const panelVisible = "(ends_at IS NULL OR ends_at > ?)"

// createAnnouncement publishes an announcement (POST /api/notifications/announcement,
// OD-unrelated, NT-1): the title is required, the body optional; starts_at/ends_at
// are optional (RFC3339, the P2-5 window) - a future starts_at means scheduled
// publish (the site-level anchor row is written first and notifyscan fans it out
// when due), while an immediate publish fans out to every enabled user.
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

// listAnnouncements is the announcement reach list (GET /api/notifications/announcements,
// P2-5 item 2): anchor rows plus per-announcement fan-out/read counts
// (n sent / m read), aggregated over the user rows joined via AnchorID.
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

// resendAnnouncement re-emits via the external channel (POST
// /api/notifications/announcements/:id/resend, P2-5 item 3): it sends another
// round of notice events for a published announcement (Herald delivers via
// TG/email) without writing new in-app rows; rate-limited to 6h per
// announcement (429); no resend once the window has closed (400).
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

// listNotifications is the full notification list (GET
// /api/notifications?limit=&type=&user_id=, for dash troubleshooting).
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

// deleteNotification deletes one notification (DELETE /api/notifications/:id).
// deleteNotification removes a notification (DELETE /api/notifications/:id): a
// normal row deletes itself; an anchor row (announcement, user_id=0) cascades
// the withdrawal - its fanned-out user rows are deleted too (the announcement
// disappears from each user's list and unread count), and one transaction
// guarantees no half-withdrawn state; an ends_at scheduled teardown is only
// invisibility, whereas this is immediate removal.
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

// panelNotifications is the current user's notification list (GET
// /api/panel/notifications?limit=&unread=1).
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

// panelUnreadCount is the current user's unread count (GET
// /api/panel/notifications/unread-count).
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

// panelMarkRead marks one notification read (POST /api/panel/notifications/:id/read):
// someone else's notification yields 404 to prevent privilege escalation;
// re-marking an already-read row is idempotent 200.
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

// panelMarkAllRead marks everything read (POST /api/panel/notifications/read-all).
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

// ---- notification preferences (NT-2) ----

// panelNotifyPrefs is the current user's notification preferences (GET /api/panel/notify-prefs).
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

// panelUpdateNotifyPrefs updates notification preferences (PUT /api/panel/notify-prefs).
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

// effectiveWarnPercent normalizes the threshold: unset/out-of-range falls back
// to the default 80 (the shared rule used by legacy rows and the scan).
func effectiveWarnPercent(v int) int {
	if v <= 0 || v > 100 {
		return 80
	}
	return v
}
