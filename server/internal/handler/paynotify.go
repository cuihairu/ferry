package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 在线支付回调（PAY-8）：Provider 验签 → 事务化三账 → 到账自动发放。
// 回调鉴权靠验签（epusdt 回调密钥），不走管理端中间件。

// epusdtNotify 是 epusdt 异步回调入口（POST /api/pay/epusdt/notify）。
// 验签失败不落任何账；结算一律 200/400，网关按此重试或停发。
func (h *Handler) epusdtNotify(c *gin.Context) {
	p, err := payment.Get("epusdt")
	if err != nil {
		fail(c, http.StatusServiceUnavailable, err) // 渠道未启用
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	cb, err := p.Verify(c.Request.Context(), raw)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	res, err := h.settlePayment(cb)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// settlePayment 回调结算（单事务）：对单校验（渠道/金额）→ 流水（同
// trade_id 重复回调幂等返回）→ 订单置 paid → 按订单发放口径执行权益
// （与卡密兑换共用 applyGrant）。
func (h *Handler) settlePayment(cb payment.Callback) (gin.H, error) {
	var out gin.H
	now := time.Now()
	err := h.db.Transaction(func(tx *gorm.DB) error {
		var order storage.PaymentOrder
		if err := tx.Where("order_no = ?", cb.OrderNo).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("订单不存在")
			}
			return err
		}
		if order.Provider != "epusdt" {
			return fmt.Errorf("订单渠道不符: %s", order.Provider)
		}
		if cb.AmountCents != order.AmountCents {
			return fmt.Errorf("回调金额 %d 分与订单 %d 分不符", cb.AmountCents, order.AmountCents)
		}
		// 幂等：同 (provider, external_id) 流水已在，或订单已 paid，直接成功返回
		var dup int64
		if err := tx.Model(&storage.PaymentTransaction{}).
			Where("provider = ? AND external_id = ?", "epusdt", cb.ExternalID).
			Count(&dup).Error; err != nil {
			return err
		}
		if dup > 0 || order.Status == "paid" {
			out = gin.H{"status": "ok", "idempotent": true}
			return nil
		}
		txn := storage.PaymentTransaction{
			OrderNo: order.OrderNo, Provider: "epusdt", ExternalID: cb.ExternalID,
			AmountCents: cb.AmountCents, Direction: "in", Raw: string(cb.Raw),
			OccurredAt: cb.PaidAt, CreatedAt: now,
		}
		if err := tx.Create(&txn).Error; err != nil {
			return err
		}
		if err := tx.Model(&storage.PaymentOrder{}).Where("id = ?", order.ID).
			Updates(map[string]any{"status": "paid", "paid_at": cb.PaidAt}).Error; err != nil {
			return err
		}
		// 到账发放：订单带发放口径才执行（空=不自动发放，走人工/对账）
		if order.GrantType == "" {
			out = gin.H{"status": "ok"}
			return nil
		}
		var u storage.User
		if err := tx.First(&u, order.UserID).Error; err != nil {
			return err
		}
		if err := applyGrant(tx, order.OrderNo, &u, order.GrantType, order.GrantValue, now,
			map[string]any{"trade_id": cb.ExternalID, "product": order.Product}); err != nil {
			return err
		}
		out = gin.H{"status": "ok", "granted": order.GrantType, "grant_value": order.GrantValue}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
