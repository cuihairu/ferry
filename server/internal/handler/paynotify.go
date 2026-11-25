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

// Online payment callback (PAY-8): provider signature check -> three
// transactional ledger writes -> automatic grant on receipt.
// Callback authentication relies on signature verification (the epusdt
// callback key) and bypasses the admin middleware.

// epusdtNotify is the epusdt async callback entry (POST /api/pay/epusdt/notify).
// A failed signature check writes nothing to the ledger; a failed settle
// answers 400 so the gateway retries; a successful settle (including an
// idempotent replay) answers the literal body ok -- the gateway decides
// callback success from the response body, not the status code, and treats
// anything but "ok" as a failure, retrying indefinitely (assimon/epusdt
// v1 callback_queue.go convention).
func (h *Handler) epusdtNotify(c *gin.Context) {
	p, err := payment.Get("epusdt")
	if err != nil {
		fail(c, http.StatusServiceUnavailable, err) // channel not enabled
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
	if _, err := h.settlePayment("epusdt", cb); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.String(http.StatusOK, "ok")
}

// settlePayment settles a callback (single transaction): order validation
// (channel/amount) -> transaction record (replays with the same trade_id
// return idempotently) -> mark the order paid -> grant entitlements per the
// order's grant spec (shared with code redemption via applyGrant).
// provider is the channel name matching the callback entry point.
func (h *Handler) settlePayment(provider string, cb payment.Callback) (gin.H, error) {
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
		if order.Provider != provider {
			return fmt.Errorf("订单渠道不符: %s", order.Provider)
		}
		if cb.AmountCents != order.AmountCents {
			return fmt.Errorf("回调金额 %d 分与订单 %d 分不符", cb.AmountCents, order.AmountCents)
		}
		// Idempotency: a transaction row for the same (provider, external_id)
		// already exists, or the order is already paid -> return success as-is
		var dup int64
		if err := tx.Model(&storage.PaymentTransaction{}).
			Where("provider = ? AND external_id = ?", provider, cb.ExternalID).
			Count(&dup).Error; err != nil {
			return err
		}
		if dup > 0 || order.Status == "paid" {
			out = gin.H{"status": "ok", "idempotent": true}
			return nil
		}
		txn := storage.PaymentTransaction{
			OrderNo: order.OrderNo, Provider: provider, ExternalID: cb.ExternalID,
			AmountCents: cb.AmountCents, Direction: "in", Raw: string(cb.Raw),
			OccurredAt: cb.PaidAt, CreatedAt: now,
		}
		if err := tx.Create(&txn).Error; err != nil {
			return err
		}
		// Marking the order paid is a conditional update (only pending can be
		// settled): concurrent callbacks with different external_ids may both
		// clear the idempotency check above, but only one flips the status.
		// The loser sees RowsAffected=0 and keeps the transaction row (the
		// money really did arrive, per the reconciliation rule) while skipping
		// the grant, which prevents double-quota. Expired or cancelled orders
		// are stopped here as well.
		res := tx.Model(&storage.PaymentOrder{}).
			Where("id = ? AND status = ?", order.ID, "pending").
			Updates(map[string]any{"status": "paid", "paid_at": cb.PaidAt})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			out = gin.H{"status": "ok", "idempotent": true}
			return nil
		}
		// Grant on receipt: only when the order carries a grant spec (empty
		// means no automatic grant, leaving it to manual work or reconciliation)
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
