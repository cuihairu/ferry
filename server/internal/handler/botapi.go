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

// TG bot 对接面（触达批 TOUCH-6，用户触达设计 §2）：bot 后端独立部署
// （不依赖面板域名存活），ferry 只做对接——本组只读接口供 bot 交互命令
// 直答（/flow /expire /order /buy）。鉴权=服务级令牌 FERRY_BOT_TOKEN
// （空=bot 面禁用），bot 按用户 tg_chat_id（TOUCH-1 绑定）定位用户。

// botUser 校验服务级令牌并按 chat_id 定位用户：Bot 面未配置统一 404
// （防探测），令牌不匹配 401，chat_id 未绑定或用户不可用 404。
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

// botSummary bot 只读直答（GET /api/bot/summary?chat_id=）：流量（重置
// 周期窗口口径，与 panel 一致）/到期/状态/联系信息失效态/最近订单/购买入口。
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
	c.JSON(http.StatusOK, gin.H{
		"username":    u.Username,
		"quota_bytes": u.QuotaBytes,
		"used_bytes":  used,
		"expires_at":  u.ExpiresAt,
		"active":      sub.UserActive(&u, used, now),
		"buy_url":     strings.TrimRight(h.cfg.BaseURL, "/") + "/panel",
		"stale":       ct.Stale, // 联系信息失效态：bot 侧提示换绑
		"orders":      items,
	})
}
