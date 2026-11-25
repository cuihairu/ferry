package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Admin side of the event outbox (HERALD-1): list and status counts. Events are produced
// by background tasks and the alerting integrations since HERALD-3; this side is read-only — channel failures show red via the failed count.

// listEvents returns the event outbox (newest first) and per-status counts.
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

// retryEvent resets a failed dead letter back to pending for immediate redelivery (a manual
// re-delivery hatch after the channel is fixed; not applicable to pending/sent).
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

// eventResultInput is the channel-dispatch receipt that Herald posts back asynchronously ("Alert Channel Design" §3):
// event_id is the ferry outbox id from the delivery payload.
type eventResultInput struct {
	EventID int64  `json:"event_id"`
	Channel string `json:"channel"`
	Status  string `json:"status"` // sent/failed
	Detail  string `json:"detail"`
}

// heraldCallback is the herald §13.5 event callback payload (native parsing per HERALD-5): the outer
// event_id is the callback event's own UUID; only delivery.event_id carries the ferry outbox
// primary key back (as a numeric string); kind=delivery_result carries the delivery result, while
// other kinds such as unsubscribe have no delivery object (ferry has no consumer for them — ack to stop herald's retry storm).
type heraldCallback struct {
	EventID  string `json:"event_id"`
	App      string `json:"app"`
	Kind     string `json:"kind"`
	Delivery *struct {
		TaskID     string `json:"task_id"`
		EventID    string `json:"event_id"`
		AudienceID string `json:"audience_id"`
		Category   string `json:"category"`
		Channel    string `json:"channel"`
		Status     string `json:"status"` // success/failed
		Error      string `json:"error"`
	} `json:"delivery"`
}

// verifyHeraldSignature validates X-Herald-Signature (herald sends `sha256=<hex>`
// HMAC-SHA256; the signature covers the raw request body, key = the configured HeraldCallbackSecret).
// Missing key = reject (P2: not verifying by default is a flaw — the receipt endpoint is a public route in the open group,
// without a key there is no way to confirm authenticity, forged receipts would be accepted by default; changed to always require verification). Once configured it
// does HMAC verification; a missing prefix or a mismatch uniformly returns 401.
func (h *Handler) verifyHeraldSignature(c *gin.Context, body []byte) bool {
	if h.cfg.HeraldCallbackSecret == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "callback secret not configured"})
		return false
	}
	sig := c.GetHeader("X-Herald-Signature")
	const prefix = "sha256="
	got := strings.TrimPrefix(sig, prefix)
	if got == sig {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return false
	}
	mac := hmac.New(sha256.New, []byte(h.cfg.HeraldCallbackSecret))
	mac.Write(body)
	if subtle.ConstantTimeCompare([]byte(got), []byte(hex.EncodeToString(mac.Sum(nil)))) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return false
	}
	return true
}

// eventResult records one per-channel delivery receipt; the event itself does not change status (the ferry→Herald leg
// already marks it sent on 2xx — channel dispatch and retries are Herald's job, the receipt is for the record only). Dual-shape
// discriminating parse (HERALD-5): event_id as a JSON number = the legacy HERALD-2 shape (existing
// production channel); as a string = the herald §13.5 event callback (the inner delivery carries the outbox id back).
func (h *Handler) eventResult(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if !h.verifyHeraldSignature(c, body) {
		return
	}
	var probe struct {
		EventID  json.RawMessage `json:"event_id"`
		Delivery json.RawMessage `json:"delivery"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(probe.EventID) > 0 && probe.EventID[0] == '"' {
		h.eventResultCallback(c, body)
		return
	}
	var in eventResultInput
	if err := json.Unmarshal(body, &in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	h.recordEventResult(c, in.EventID, in.Channel, in.Status, in.Detail)
}

// eventResultCallback parses the §13.5 callback: delivery.success→sent, failed→
// failed (reason lands in detail); a missing delivery (non-delivery callbacks such as unsubscribe) is acked
// and ignored; a non-numeric delivery.event_id or a missing channel/status maps to 400.
func (h *Handler) eventResultCallback(c *gin.Context, body []byte) {
	var cb heraldCallback
	if err := json.Unmarshal(body, &cb); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if cb.Delivery == nil {
		c.JSON(http.StatusOK, gin.H{"ok": true, "ignored": true})
		return
	}
	id, err := strconv.ParseInt(cb.Delivery.EventID, 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("delivery.event_id must be a numeric ferry outbox id"))
		return
	}
	var status string
	switch cb.Delivery.Status {
	case "success":
		status = herald.StatusSent
	case "failed":
		status = herald.StatusFailed
	default:
		fail(c, http.StatusBadRequest, errors.New("delivery.status must be success/failed"))
		return
	}
	h.recordEventResult(c, id, cb.Delivery.Channel, status, cb.Delivery.Error)
}

// recordEventResult is the shared persistence tail for both shapes: checks the event exists, writes
// event_deliveries, and links the touch tasks (TOUCH-5).
func (h *Handler) recordEventResult(c *gin.Context, eventID int64, channel, status, detail string) {
	if eventID <= 0 {
		fail(c, http.StatusBadRequest, errors.New("event_id is required"))
		return
	}
	if channel == "" {
		fail(c, http.StatusBadRequest, errors.New("channel is required"))
		return
	}
	if status != herald.StatusSent && status != herald.StatusFailed {
		fail(c, http.StatusBadRequest, errors.New("status must be sent/failed"))
		return
	}
	var ev storage.Event
	if err := h.db.Select("id").First(&ev, eventID).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("event not found"))
		return
	}
	row := storage.EventDelivery{
		EventID: eventID, Channel: channel, Status: status,
		Detail: detail, At: time.Now(),
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	h.touchResult(eventID, status, detail) // touch-task linkage (TOUCH-5)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- Channel delivery dashboard and self-test (HC-1/HC-2/HC-3, "Alert Channel Design" §6 P2 rows) ----

// listEventDeliveries is the delivery receipt dashboard (GET /api/events/deliveries): recent receipt rows
// (newest first) + per-channel aggregation (sent/failed counts, latest failure detail) — the passive side of
// channel-fault self-diagnosis, giving before/after evidence when fixing a channel.
func (h *Handler) listEventDeliveries(c *gin.Context) {
	limit := 100
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-200"))
			return
		}
		limit = n
	}
	q := h.db.Model(&storage.EventDelivery{}).Order("id DESC").Limit(limit)
	if v := c.Query("channel"); v != "" {
		q = q.Where("channel = ?", v)
	}
	rows := []storage.EventDelivery{}
	if err := q.Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	// Full per-channel aggregation (receipt rows are small in volume, plain GROUP BY).
	var agg []struct {
		Channel string
		Sent    int64
		Failed  int64
	}
	if err := h.db.Model(&storage.EventDelivery{}).
		Select("channel, COALESCE(SUM(status = 'sent'), 0) AS sent, COALESCE(SUM(status = 'failed'), 0) AS failed").
		Group("channel").Order("channel").Scan(&agg).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	channels := make([]gin.H, 0, len(agg))
	for _, a := range agg {
		health := "healthy"
		if a.Sent == 0 && a.Failed > 0 {
			health = "down"
		} else if a.Failed > 0 {
			health = "degraded"
		}
		item := gin.H{"channel": a.Channel, "sent": a.Sent, "failed": a.Failed, "health": health}
		// Latest failure detail (one query per channel; the channel count is single-digit).
		var last storage.EventDelivery
		if err := h.db.Where("channel = ? AND status = ?", a.Channel, herald.StatusFailed).
			Order("id DESC").First(&last).Error; err == nil {
			item["last_failed_at"] = last.At
			item["last_detail"] = last.Detail
		}
		channels = append(channels, item)
	}
	c.JSON(http.StatusOK, gin.H{"rows": rows, "channels": channels})
}

// testEvent is the channel self-test (POST /api/events/test): emits one system_test event through the
// full outbox→Herald→receipt chain (reusing the backoff-retry and dead-letter semantics, without injecting a Sender).
// The delivery result shows up shortly after in the receipt dashboard and the outbox — the "active side" of self-testing.
func (h *Handler) testEvent(c *gin.Context) {
	ev, err := herald.Emit(h.db, herald.EmitInput{
		Kind: "system_test", Severity: "info", Target: "admin",
		Title: "ferry 通道自检",
		Body:  "通道自检测试事件（" + time.Now().Format("2006-01-02 15:04:05") + "），收到即代表投递链路正常，可忽略。",
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, ev)
}

// listEventSamples is the notification sample preview (GET /api/events/samples): for each kind it takes the most
// recent actually-persisted event as the sample — zero drift (real outbound title/body shapes, not template copies),
// so admins can see what each alert kind looks like before sending a test.
func (h *Handler) listEventSamples(c *gin.Context) {
	var kinds []string
	if err := h.db.Model(&storage.Event{}).Distinct().Order("kind").Pluck("kind", &kinds).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]gin.H, 0, len(kinds))
	for _, k := range kinds {
		var ev storage.Event
		if err := h.db.Where("kind = ?", k).Order("id DESC").First(&ev).Error; err != nil {
			continue
		}
		out = append(out, gin.H{
			"kind": ev.Kind, "severity": ev.Severity, "title": ev.Title,
			"body": ev.Body, "status": ev.Status, "occurred_at": ev.OccurredAt,
		})
	}
	c.JSON(http.StatusOK, out)
}
