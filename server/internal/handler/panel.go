package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/sub"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 用户门户（PAY-7）：以订阅令牌作凭据的无状态身份。
// P0 口径——不引入密码与服务端会话（《安全设计》的用户名密码+2FA 对齐 P1-1，
// 届时收紧为登录会话，本组接口的身份来源随之替换，路由载荷不变）。

// panelUser 解析请求身份：Authorization: Bearer <sub_token>，缺省回落 X-Ferry-Token。
// 令牌缺失、未匹配、未启用统一 404（令牌视同吊销，防枚举口径与 /sub/:token 一致）。
func (h *Handler) panelUser(c *gin.Context) (storage.User, bool) {
	auth := c.GetHeader("Authorization")
	token := ""
	if strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	if token == "" {
		token = strings.TrimSpace(c.GetHeader("X-Ferry-Token"))
	}
	if token == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return storage.User{}, false
	}
	var u storage.User
	if err := h.db.Where("sub_token = ?", token).First(&u).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusInternalServerError, err)
			return storage.User{}, false
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return storage.User{}, false
	}
	if !u.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return storage.User{}, false
	}
	return u, true
}

// panelMe 返回当前用户的用量与状态（GET /api/panel/me）。
// used_bytes 为 traffic_logs 累计，active 复用订阅侧口径（到期/超限一致判定）。
func (h *Handler) panelMe(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	var usage trafficUsage
	if err := h.db.Model(&storage.TrafficLog{}).
		Select("COALESCE(SUM(rx_bytes),0) AS rx, COALESCE(SUM(tx_bytes),0) AS tx").
		Where("user_id = ?", u.ID).Scan(&usage).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	used := usage.Rx + usage.Tx
	c.JSON(http.StatusOK, gin.H{
		"id":          u.ID,
		"username":    u.Username,
		"sub_token":   u.SubToken,
		"quota_bytes": u.QuotaBytes,
		"used_bytes":  used,
		"expires_at":  u.ExpiresAt,
		"active":      sub.UserActive(&u, used, time.Now()),
		"over_quota":  u.QuotaBytes > 0 && used >= u.QuotaBytes,
		"created_at":  u.CreatedAt,
	})
}

// panelRedeem 卡密兑换（POST /api/panel/redeem）：身份取自令牌，user_id 不可传。
// 限速与统一失败文案复用 dash 侧兑换口径（《支付设计》§3.3）。
func (h *Handler) panelRedeem(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, errRedeemFailed)
		return
	}
	code := normalizeCardCode(in.Code)
	if code == "" {
		fail(c, http.StatusBadRequest, errRedeemFailed)
		return
	}
	ip := c.ClientIP()
	if !h.redeemLimiter.Allow(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试过于频繁，请稍后再试"})
		return
	}
	now := time.Now()
	result, err := h.redeemTx(code, u.ID, now)
	if err != nil {
		if errors.Is(err, errRedeemFailed) {
			h.redeemLimiter.RecordFailure(ip)
			h.bumpCardFailCount(code)
			fail(c, http.StatusBadRequest, errRedeemFailed)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	h.redeemLimiter.Reset(ip)
	c.JSON(http.StatusOK, result)
}
