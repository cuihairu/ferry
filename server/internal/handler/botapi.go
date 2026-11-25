package handler

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/sub"
	"github.com/gin-gonic/gin"
)

// TG bot integration surface (outreach batch TOUCH-6, user outreach design §2):
// the bot backend is deployed independently (it does not depend on the panel
// domain being alive), and ferry only provides the integration -- this group of
// read-only endpoints lets bot interactive commands answer directly (/flow
// /expire /order /buy). Auth = the service-level token FERRY_BOT_TOKEN (empty
// means the bot surface is disabled); the bot locates a user by tg_chat_id
// (bound via TOUCH-1).

// botUser verifies the service-level token and locates the user by chat_id:
// an unconfigured bot surface is a uniform 404 (to foil probing), a mismatched
// token is 401, and an unbound chat_id or an unusable user is 404.
func (h *Handler) botUser(c *gin.Context) (storage.User, storage.UserContact, bool) {
	token := h.cfg.BotToken
	if token == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return storage.User{}, storage.UserContact{}, false
	}
	auth := c.GetHeader("Authorization")
	got := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	if got == "" {
		got = strings.TrimSpace(c.GetHeader("X-Ferry-Bot-Token"))
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return storage.User{}, storage.UserContact{}, false
	}
	chatID := strings.TrimSpace(c.Query("chat_id"))
	if chatID == "" {
		fail(c, http.StatusBadRequest, errors.New("chat_id is required"))
		return storage.User{}, storage.UserContact{}, false
	}
	var ct storage.UserContact
	if err := h.db.Where("tg_chat_id = ?", chatID).First(&ct).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not bound"})
		return storage.User{}, storage.UserContact{}, false
	}
	var u storage.User
	if err := h.db.First(&u, ct.UserID).Error; err != nil || !u.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return storage.User{}, storage.UserContact{}, false
	}
	return u, ct, true
}

// botSummary answers a bot read-only request (GET /api/bot/summary?chat_id=):
// traffic (reset-cycle window semantics, matching the panel) / expiry / status /
// contact-stale flag / recent orders / purchase entry.
func (h *Handler) botSummary(c *gin.Context) {
	u, ct, ok := h.botUser(c)
	if !ok {
		return
	}
	now := time.Now()
	var usage trafficUsage
	if err := h.usageQuery(u.ID, u.ResetCycle, now).
		Select("COALESCE(SUM(rx_bytes),0) AS rx, COALESCE(SUM(tx_bytes),0) AS tx").
		Scan(&usage).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var orders []storage.PaymentOrder
	if err := h.db.Where("user_id = ?", u.ID).Order("id DESC").Limit(3).Find(&orders).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]gin.H, 0, len(orders))
	for _, o := range orders {
		items = append(items, gin.H{
			"order_no": o.OrderNo, "product": o.Product,
			"amount_cents": o.AmountCents, "status": o.Status, "created_at": o.CreatedAt,
		})
	}
	used := usage.Rx + usage.Tx
	st := sub.UserStateOf(&u, used, now)
	c.JSON(http.StatusOK, gin.H{
		"username":    u.Username,
		"quota_bytes": u.QuotaBytes,
		"used_bytes":  used,
		"expires_at":  u.ExpiresAt,
		"active":      st.Status == sub.StatusActive,
		// Split gate state (G3): the primary cause of an inactive subscription.
		"status":  st.Status,
		"buy_url": strings.TrimRight(h.cfg.BaseURL, "/") + "/panel",
		"stale":   ct.Stale, // contact-stale flag: the bot side prompts a rebinding
		"orders":  items,
	})
}
