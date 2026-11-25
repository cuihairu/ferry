package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
	"github.com/cuihairu/ferry/server/internal/save"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/sub"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// User portal (PAY-7): stateless identity with the subscription token as
// credential.
// P0 stance - no passwords and no server-side sessions (the username/password+2FA
// in "Security Design" is aligned with P1-1; at that point this tightens to a
// login session, and the identity source of this endpoint group is swapped in
// while the route payloads stay unchanged).

// panelUser resolves the request identity: Authorization: Bearer <sub_token>,
// falling back to X-Ferry-Token. A missing, unmatched, or disabled token is
// uniformly 404 (the token is treated as revoked, the same anti-enumeration
// stance as /sub/:token).
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

// panelMe returns the current user's usage and status (GET /api/panel/me).
// used_bytes is accumulated from traffic_logs; active reuses the subscription-side
// rule (consistent expiry/over-quota judgment).
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
	// Device-side usage (P2-4 visibility closure): when a subscription request
	// gets 403 over the limit, the user can see the registered counts and the
	// caps in the panel - devices = registered hwid count (only counted when the
	// client reports), ips = count registered within the 24h window.
	var devices, ips int64
	if err := h.db.Model(&storage.SubDevice{}).
		Where("user_id = ? AND kind = ?", u.ID, "hwid").
		Count(&devices).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.Model(&storage.SubDevice{}).
		Where("user_id = ? AND kind = ? AND last_seen > ?", u.ID, "ip", time.Now().Add(-subDeviceIPWindow)).
		Count(&ips).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	st := sub.UserStateOf(&u, used, time.Now())
	c.JSON(http.StatusOK, gin.H{
		"id":          u.ID,
		"username":    u.Username,
		"sub_token":   u.SubToken,
		"quota_bytes": u.QuotaBytes,
		"used_bytes":  used,
		"expires_at":  u.ExpiresAt,
		"active":      st.Status == sub.StatusActive,
		"over_quota":  st.Limited,
		// Split gate state (G3): the primary cause plus the two independent
		// judgments, so the portal can show why access is gone.
		"status":       st.Status,
		"expired":      st.Expired,
		"limited":      st.Limited,
		"created_at":   u.CreatedAt,
		"bw_up_mbps":   u.BwUpMbps,
		"bw_down_mbps": u.BwDownMbps,
		"device_limit": u.DeviceLimit,
		"ip_limit":     u.IPLimit,
		"devices":      devices,
		"ips":          ips,
	})
}

// panelRedeem redeems a card code (POST /api/panel/redeem): identity comes from
// the token, user_id cannot be passed.
// Rate limiting and the unified failure message reuse the dash-side redeem
// stance ("Payment Design" §3.3).
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

// panelOrders is the order center (GET /api/panel/orders, OD-1): the current
// user's orders in reverse chronological order, each order merged with its
// grant records (provider=card merges; once epusdt and others are live the
// shape is the same, with product/amount passed through verbatim).
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
	q := h.db.Where("user_id = ?", u.ID)
	// Filter by source (operations design §record scope): provider=card
	// means card-code redemptions; the default is the full set, coexisting with
	// the "single list display".
	if v := c.Query("provider"); v != "" {
		q = q.Where("provider = ?", v)
	}
	if err := q.Order("id DESC").Limit(limit).Find(&orders).Error; err != nil {
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
			"refund_at":    orders[i].RefundAt,
			"grants":       items,
		})
	}
	c.JSON(http.StatusOK, out)
}

// ---- online checkout (PAY-11, the epusdt leg) ----

// panelProduct is a portal sellable product row: it comes from a card-code
// batch, and a batch counts as listed when it has a price (price_cents>0), is
// not expired, and still has remaining codes.
type panelProduct struct {
	ID         uint   `json:"id"`
	Name       string `json:"name"`
	PriceCents int64  `json:"price_cents"`
	GrantType  string `json:"grant_type"`
	GrantValue int64  `json:"grant_value"`
	Remaining  int64  `json:"remaining"`
}

// panelChannels lists the available payment channels (GET /api/panel/channels):
// the channel picker renders from registration state, and a placeholder channel
// (NewDisabled, awaiting merchant credentials) is marked disabled (SHOP-1).
func (h *Handler) panelChannels(c *gin.Context) {
	if _, ok := h.panelUser(c); !ok {
		return
	}
	out := make([]gin.H, 0)
	for _, ch := range payment.Channels() {
		out = append(out, gin.H{"name": ch.Name, "enabled": ch.Enabled})
	}
	c.JSON(http.StatusOK, out)
}

// panelProducts is the sellable product list (GET /api/panel/products).
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

// panelCreateOrder places a portal order (POST /api/panel/orders): {batch_id, provider}.
// Pricing comes from the batch price_cents, the client cannot pass an amount;
// the order is first written as pending to anchor the three ledgers, then
// callback settlement (settlePayment) sets it paid along the PAY-8 path and
// auto-grants per the batch rule.
func (h *Handler) panelCreateOrder(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	var in struct {
		BatchID    uint   `json:"batch_id"`
		Provider   string `json:"provider"`
		CouponCode string `json:"coupon_code"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.BatchID == 0 || in.Provider == "" {
		fail(c, http.StatusBadRequest, errors.New("batch_id 与 provider 必填"))
		return
	}
	// Placing an order hits the upstream gateway, so IP rate limiting is a safety
	// net (only throttles, does not record failures).
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
	// Best-discount pick (DS §3.3): a single order has one discount source - the
	// code and campaign candidates each compute their discount and the larger
	// one is carried into the order; a losing code is not redeemed (no side
	// effects). The code is first read-only validated (window/threshold/scope/
	// usage cap, failing straight to the business message), and after the
	// gateway order succeeds, "redeem + order row" land in the same transaction -
	// a conditional UPDATE on redemption prevents concurrent over-issue.
	paid := batch.PriceCents
	code := strings.ToUpper(strings.TrimSpace(in.CouponCode))
	var cp *storage.Coupon
	var camp *storage.Campaign
	if code != "" {
		c0, err := couponCheck(h.db, code, &u, &batch, batch.PriceCents, now)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		cp = &c0
		paid = couponDiscount(cp.Kind, cp.Value, paid)
	}
	camp, campPaid, err := h.bestCampaign(h.db, &u, &batch, batch.PriceCents, now)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if camp != nil && campPaid < paid {
		cp = nil // campaign wins: the code is not redeemed
		paid = campPaid
	} else {
		camp = nil // the code wins, or there is no campaign
	}
	rcpt, err := provider.CreateOrder(c.Request.Context(), payment.Order{
		OrderNo: orderNo, UserID: int64(u.ID), AmountCents: paid,
		Product: batch.Name, CreatedAt: now,
	})
	if err != nil {
		fail(c, http.StatusBadGateway, err) // upstream gateway failure
		return
	}
	order := storage.PaymentOrder{
		OrderNo: orderNo, UserID: u.ID, Provider: in.Provider,
		AmountCents: paid, ListAmountCents: batch.PriceCents,
		Product: batch.Name, Status: "pending",
		GrantType: batch.GrantType, GrantValue: batch.GrantValue,
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if cp != nil {
			burnPaid, burned, err2 := applyCouponTx(tx, code, &u, &batch, batch.PriceCents, now)
			if err2 != nil {
				// A failure after pre-check passed can only be a concurrent usage-cap race;
				// roll back the whole order (code included).
				return err2
			}
			order.PromoCode = burned.Code
			order.PromoSnapshot = couponSnapshot(burned.Code, burned.Kind, burned.Value, batch.PriceCents, burnPaid)
		} else if camp != nil {
			rules, err2 := h.parseCampaignRules(camp.Rules)
			if err2 != nil {
				return err2
			}
			order.PromoSnapshot = campaignSnapshot(camp, rules, batch.PriceCents, paid)
		}
		return tx.Create(&order).Error
	}); err != nil {
		// The gateway order exists but the local one did not land: the user simply has
		// no pay_url and the gateway order expires on its own.
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"order_no":     orderNo,
		"provider":     in.Provider,
		"product":      batch.Name,
		"amount_cents": paid,
		"pay_url":      rcpt.PayURL,
		"external_id":  rcpt.ExternalID,
		"expires_at":   rcpt.ExpiresAt,
	})
}

// panelOrderStatus is a single order's status (GET /api/panel/orders/:order_no):
// visible only to its owner (anyone else's order number is uniformly 404,
// anti-enumeration), merged with its grant records.
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
		"refund_at":    order.RefundAt,
		"grants":       items,
	})
}

// panelSavings is the user-side "saved for you" summary (GET /api/panel/savings, SAVE-8).
// Node-level savings counters (save_stats) come from agent egress counting and
// carry no user identity, so they are prorated by the user's share of recorded
// traffic on that node for the month (a reviewable estimate; a node with no
// recorded user traffic this month is not allocated).
// Window = calendar month to date (UTC month start, consistent with save_stats'
// UTC day aggregation key in the same pipeline; billing/traffic judgment windows
// use the server's local timezone, see quota and toucher). The cache-hit column
// will enter the response automatically once SAVE-3 metrics feed in; reserved
// for now.
func (h *Handler) panelSavings(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	monthStart := time.Now().UTC().Format("2006-01") + "-01"
	direct, cacheHit, blocked, err := save.UserSavings(h.db, u.ID, monthStart)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"window":          monthStart,
		"direct_bytes":    direct,
		"cache_hit_bytes": cacheHit,
		"blocked_bytes":   blocked,
	})
}
