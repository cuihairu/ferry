package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
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
	if err := h.usageQuery(u.ID, u.ResetCycle, time.Now()).
		Select("COALESCE(SUM(rx_bytes),0) AS rx, COALESCE(SUM(tx_bytes),0) AS tx").
		Scan(&usage).Error; err != nil {
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

// panelOrders 订单中心（GET /api/panel/orders，OD-1）：当前用户的订单按时间倒序，
// 每单归并其发放记录（provider=card 归并；epusdt 等上线后同形返回，product/金额原样透出）。
func (h *Handler) panelOrders(c *gin.Context) {
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
	var orders []storage.PaymentOrder
	if err := h.db.Where("user_id = ?", u.ID).Order("id DESC").Limit(limit).Find(&orders).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]gin.H, 0, len(orders))
	for i := range orders {
		var grants []storage.Grant
		if err := h.db.Where("order_no = ?", orders[i].OrderNo).Order("id").Find(&grants).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		items := make([]gin.H, 0, len(grants))
		for j := range grants {
			items = append(items, gin.H{
				"grant_type":  grants[j].GrantType,
				"grant_value": grants[j].GrantValue,
			})
		}
		out = append(out, gin.H{
			"order_no":     orders[i].OrderNo,
			"provider":     orders[i].Provider,
			"product":      orders[i].Product,
			"amount_cents": orders[i].AmountCents,
			"status":       orders[i].Status,
			"created_at":   orders[i].CreatedAt,
			"paid_at":      orders[i].PaidAt,
			"grants":       items,
		})
	}
	c.JSON(http.StatusOK, out)
}

// ---- 在线下单（PAY-11，对 epusdt 段）----

// panelProduct 是门户可售商品行：来自卡密批次，设了价（price_cents>0）
// 且未过期、仍有剩余卡密的批次视为上架。
type panelProduct struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	PriceCents int64  `json:"price_cents"`
	GrantType  string `json:"grant_type"`
	GrantValue int64  `json:"grant_value"`
	Remaining  int64  `json:"remaining"`
}

// panelProducts 可售商品列表（GET /api/panel/products）。
func (h *Handler) panelProducts(c *gin.Context) {
	if _, ok := h.panelUser(c); !ok {
		return
	}
	var batches []storage.CardBatch
	if err := h.db.Where("price_cents > 0 AND (expired_at IS NULL OR expired_at > ?)", time.Now()).
		Order("id DESC").Find(&batches).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	remaining := map[uint]int64{}
	var rows []struct {
		BatchID uint
		Cnt     int64
	}
	if err := h.db.Model(&storage.CardCode{}).
		Select("batch_id, COUNT(*) AS cnt").Where("status = ?", "unused").
		Group("batch_id").Scan(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	for _, r := range rows {
		remaining[r.BatchID] = r.Cnt
	}
	out := make([]panelProduct, 0, len(batches))
	for _, b := range batches {
		if remaining[b.ID] == 0 {
			continue
		}
		out = append(out, panelProduct{
			ID: b.ID, Name: b.Name, PriceCents: b.PriceCents,
			GrantType: b.GrantType, GrantValue: b.GrantValue, Remaining: remaining[b.ID],
		})
	}
	c.JSON(http.StatusOK, out)
}

// panelCreateOrder 门户下单（POST /api/panel/orders）：{batch_id, provider}。
// 定价取批次 price_cents，客户端不可传金额；订单先落 pending 作三账锚点，
// 回调结算（settlePayment）照 PAY-8 路径置 paid 并按批次口径自动发放。
func (h *Handler) panelCreateOrder(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	var in struct {
		BatchID  uint   `json:"batch_id"`
		Provider string `json:"provider"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.BatchID == 0 || in.Provider == "" {
		fail(c, http.StatusBadRequest, errors.New("batch_id 与 provider 必填"))
		return
	}
	// 下单会打到上游网关，按 IP 限速兜底（只限频不记失败）。
	ip := c.ClientIP()
	if !h.orderLimiter.Allow(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "下单过于频繁，请稍后再试"})
		return
	}
	provider, err := payment.Get(in.Provider)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("收款渠道不可用"))
		return
	}
	var batch storage.CardBatch
	if err := h.db.First(&batch, in.BatchID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusBadRequest, errors.New("商品不可售"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	now := time.Now()
	if batch.PriceCents <= 0 || (batch.ExpiredAt != nil && !batch.ExpiredAt.After(now)) {
		fail(c, http.StatusBadRequest, errors.New("商品不可售"))
		return
	}
	var sellable int64
	if err := h.db.Model(&storage.CardCode{}).
		Where("batch_id = ? AND status = ?", batch.ID, "unused").
		Count(&sellable).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if sellable == 0 {
		fail(c, http.StatusBadRequest, errors.New("商品已售罄"))
		return
	}
	orderNo := fmt.Sprintf("%s-%d-%d", in.Provider, u.ID, now.UnixNano())
	rcpt, err := provider.CreateOrder(c.Request.Context(), payment.Order{
		OrderNo: orderNo, UserID: int64(u.ID), AmountCents: batch.PriceCents,
		Product: batch.Name, CreatedAt: now,
	})
	if err != nil {
		fail(c, http.StatusBadGateway, err) // 上游网关失败
		return
	}
	order := storage.PaymentOrder{
		OrderNo: orderNo, UserID: u.ID, Provider: in.Provider,
		AmountCents: batch.PriceCents, Product: batch.Name, Status: "pending",
		GrantType: batch.GrantType, GrantValue: batch.GrantValue,
	}
	if err := h.db.Create(&order).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"order_no":     orderNo,
		"provider":     in.Provider,
		"product":      batch.Name,
		"amount_cents": batch.PriceCents,
		"pay_url":      rcpt.PayURL,
		"external_id":  rcpt.ExternalID,
		"expires_at":   rcpt.ExpiresAt,
	})
}

// panelOrderStatus 单笔订单状态（GET /api/panel/orders/:order_no）：
// 仅本人可见（他人订单号统一 404，防枚举），归并发放记录。
func (h *Handler) panelOrderStatus(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	var order storage.PaymentOrder
	if err := h.db.Where("order_no = ? AND user_id = ?", c.Param("order_no"), u.ID).
		First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var grants []storage.Grant
	if err := h.db.Where("order_no = ?", order.OrderNo).Order("id").Find(&grants).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]gin.H, 0, len(grants))
	for i := range grants {
		items = append(items, gin.H{
			"grant_type":  grants[i].GrantType,
			"grant_value": grants[i].GrantValue,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"order_no":     order.OrderNo,
		"provider":     order.Provider,
		"product":      order.Product,
		"amount_cents": order.AmountCents,
		"status":       order.Status,
		"grant_type":   order.GrantType,
		"grant_value":  order.GrantValue,
		"created_at":   order.CreatedAt,
		"paid_at":      order.PaidAt,
		"grants":       items,
	})
}
