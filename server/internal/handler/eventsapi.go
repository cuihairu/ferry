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

// heraldCallback 是 herald §13.5 事件回调载荷（HERALD-5 原生解析）：外层
// event_id 是回调事件自身的 UUID，delivery.event_id 才回带 ferry outbox
// 主键（字符串数字）；kind=delivery_result 带投递结果，unsubscribe 等其他
// kind 无 delivery 对象（ferry 无对应消费面，ack 掉防 herald 重试刷）。
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

// verifyHeraldSignature 校验 X-Herald-Signature（herald 侧 `sha256=<hex>`
// HMAC-SHA256，签名覆盖原始请求体，密钥=配置的 HeraldCallbackSecret）。
// 未配置密钥=不验签（HERALD-2 的网络边界隔离口径延续，既有回执通道不断）；
// 配置后强制验签，缺前缀或不匹配统一 401。
func (h *Handler) verifyHeraldSignature(c *gin.Context, body []byte) bool {
	if h.cfg.HeraldCallbackSecret == "" {
		return true
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

// eventResult 落一条通道维度投递回执；事件本体状态不动（ferry→Herald 腿
// 2xx 即已置 sent，通道分发与重试由 Herald 管，回执只做留痕）。双形状
// 判别解析（HERALD-5）：event_id 是 JSON 数字=HERALD-2 旧形状（线上既有
// 通道），是字符串=herald §13.5 事件回调（delivery 内层回带 outbox id）。
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

// eventResultCallback 解析 §13.5 回调：delivery.success→sent、failed→
// failed（原因落 detail）；delivery 缺失（unsubscribe 等非投递回调）ack
// 忽略；delivery.event_id 非数字或缺 channel/status 归 400。
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

// recordEventResult 是两种形状共用的落库尾：校验事件存在、落
// event_deliveries、联动触达任务（TOUCH-5）。
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
	h.touchResult(eventID, status, detail) // 触达任务联动（TOUCH-5）
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
