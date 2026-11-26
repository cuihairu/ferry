package handler

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// orderPendingTimeout is the unified order timeout rule (F-1303): a pending
// order not settled within 30 minutes of creation is expired by the sweep.
// One clock, one rule -- both timeout and cancellation only ever move a
// pending order; paid/failed/expired/cancelled/refunded are terminal. The
// settle side enforces the same boundary from the other direction (the
// conditional update in settlePayment only flips pending, so a callback
// arriving after expiry can never revive an order -- the money row lands in
// the ledger and payrecon flags it for review).
const orderPendingTimeout = 30 * time.Minute

// OrderSweep expires stale pending orders (F-1303): one batched conditional
// update, only pending rows inside the timeout horizon. Until this sweeper
// existed pending rows lived forever (the coupon per-user cap even carries a
// comment about it); expiry also frees coupon per-user caps and stock reads
// from counting undead orders.
func OrderSweep(db *gorm.DB, now time.Time) (int64, error) {
	res := db.Model(&storage.PaymentOrder{}).
		Where("status = ? AND created_at < ?", "pending", now.Add(-orderPendingTimeout)).
		Update("status", "expired")
	return res.RowsAffected, res.Error
}

// OrderLoop is the timed order sweep (matching the Loop convention of
// notifyscan/cert); a minute-order magnitude is fine -- the sweep is one
// indexed UPDATE and the timeout is far larger than the period.
func OrderLoop(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := OrderSweep(db, time.Now()); err != nil {
				logger.Printf("ordersweep: %v", err)
			}
		}
	}
}

// cancelPanelOrder handles POST /api/panel/orders/:order_no/cancel (F-1303):
// the order owner cancels their own pending order. Same rule as the sweep --
// pending is the only cancellable state; anything else is a conflict, and a
// foreign order_no is indistinguishable from a missing one (owner-scoped
// lookup, no enumeration).
func (h *Handler) cancelPanelOrder(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	res := h.db.Model(&storage.PaymentOrder{}).
		Where("order_no = ? AND user_id = ? AND status = ?", c.Param("order_no"), u.ID, "pending").
		Update("status", "cancelled")
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusConflict, errors.New("订单不可取消"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}
