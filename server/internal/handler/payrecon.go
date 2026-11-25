package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/ringlog"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// fmtOrphanAmount is the summary text for an orphan transaction.
func fmtOrphanAmount(cents int64) string {
	return fmt.Sprintf("%d 分", cents)
}

// Three-ledger reconciliation (PAY-9): orders / transactions / grants are
// grouped by order and returned with the missing legs flagged, plus orphan
// records (no matching order) and running totals, for the dash
// reconciliation view.

// reconcileRow is the three-ledger view of one order: it hangs the order's
// transactions and grants off the order, and missing lists the wording of
// the legs that do not add up.
type reconcileRow struct {
	storage.PaymentOrder
	Transactions []storage.PaymentTransaction `json:"transactions"`
	Grants       []storage.Grant              `json:"grants"`
	Missing      []string                     `json:"missing"`
}

// reconcileOrphan is an orphan record: a transaction or grant whose order cannot be found.
type reconcileOrphan struct {
	Kind     string `json:"kind"` // transaction / grant
	OrderNo  string `json:"order_no"`
	Provider string `json:"provider"`
	Detail   string `json:"detail"`
}

// listReconcile returns three-ledger reconciliation data (GET /api/payments/reconcile?limit=).
func (h *Handler) listReconcile(c *gin.Context) {
	limit := 200
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-500"))
			return
		}
		limit = n
	}
	var orders []storage.PaymentOrder
	if err := h.db.Order("id DESC").Limit(limit).Find(&orders).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	orderNos := make([]string, 0, len(orders))
	byNo := map[string]*reconcileRow{}
	for _, o := range orders {
		orderNos = append(orderNos, o.OrderNo)
		byNo[o.OrderNo] = &reconcileRow{PaymentOrder: o}
	}
	if len(orderNos) > 0 {
		var txns []storage.PaymentTransaction
		if err := h.db.Where("order_no IN ?", orderNos).Order("id").Find(&txns).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		for _, t := range txns {
			if row, ok := byNo[t.OrderNo]; ok {
				row.Transactions = append(row.Transactions, t)
			}
		}
		var grants []storage.Grant
		if err := h.db.Where("order_no IN ?", orderNos).Order("id").Find(&grants).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		for _, g := range grants {
			if row, ok := byNo[g.OrderNo]; ok {
				row.Grants = append(row.Grants, g)
			}
		}
	}
	// Missing legs: paid/refunded with no transaction, paid with a grant spec
	// but no grant, pending yet carrying a transaction.
	// The three-ledger completeness check uses the same rule for paid
	// (refunded orders should have transaction and grant records to show).
	rows := make([]reconcileRow, 0, len(orders))
	for _, o := range orders {
		row := byNo[o.OrderNo]
		if o.Status == "paid" || o.Status == "refunded" {
			if len(row.Transactions) == 0 {
				note := "缺支付流水"
				if o.Status == "refunded" {
					note = "已退款但缺支付流水"
				}
				row.Missing = append(row.Missing, note)
			}
			if o.GrantType != "" && len(row.Grants) == 0 {
				row.Missing = append(row.Missing, "缺发放")
			}
		}
		if o.Status == "pending" && len(row.Transactions) > 0 {
			row.Missing = append(row.Missing, "有流水未结算")
		}
		rows = append(rows, *row)
	}

	// Orphans: the order a transaction or grant points at does not exist
	// (a hard reconciliation fault, listed separately).
	var orphans []reconcileOrphan
	var orphanTxns []storage.PaymentTransaction
	if err := h.db.Where("order_no NOT IN (?)", h.db.Model(&storage.PaymentOrder{}).Select("order_no")).
		Order("id DESC").Limit(50).Find(&orphanTxns).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	for _, t := range orphanTxns {
		orphans = append(orphans, reconcileOrphan{
			Kind: "transaction", OrderNo: t.OrderNo, Provider: t.Provider,
			Detail: fmtOrphanAmount(t.AmountCents),
		})
	}
	var orphanGrants []storage.Grant
	if err := h.db.Where("order_no NOT IN (?)", h.db.Model(&storage.PaymentOrder{}).Select("order_no")).
		Order("id DESC").Limit(50).Find(&orphanGrants).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	for _, g := range orphanGrants {
		orphans = append(orphans, reconcileOrphan{
			Kind: "grant", OrderNo: g.OrderNo, Provider: "",
			Detail: g.GrantType,
		})
	}

	// Totals are computed over the whole database (P2: the old implementation
	// summed only the current page's limit rows, and the dash displayed them
	// as gross revenue -- once orders outgrew one page the totals silently
	// shrank). Rows within the page are still returned per limit, but the
	// totals now aggregate the full table: receipts and refunds bucketed by
	// status, transactions and grants as full-table sums.
	var sum struct {
		PaidOrders     int64
		PaidCents      int64
		RefundedOrders int64
		RefundedCents  int64
	}
	if err := h.db.Model(&storage.PaymentOrder{}).Select(
		"COALESCE(SUM(CASE WHEN status = 'paid' THEN 1 ELSE 0 END), 0) AS paid_orders, " +
			"COALESCE(SUM(CASE WHEN status = 'paid' THEN amount_cents ELSE 0 END), 0) AS paid_cents, " +
			"COALESCE(SUM(CASE WHEN status = 'refunded' THEN 1 ELSE 0 END), 0) AS refunded_orders, " +
			"COALESCE(SUM(CASE WHEN status = 'refunded' THEN amount_cents ELSE 0 END), 0) AS refunded_cents",
	).Scan(&sum).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var txnSum struct {
		TxnCents int64
	}
	if err := h.db.Model(&storage.PaymentTransaction{}).
		Select("COALESCE(SUM(amount_cents), 0) AS txn_cents").Scan(&txnSum).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var grantCount int64
	if err := h.db.Model(&storage.Grant{}).Count(&grantCount).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"orders":  rows,
		"orphans": orphans,
		"summary": gin.H{
			"paid_orders":     sum.PaidOrders,
			"paid_cents":      sum.PaidCents,
			"refunded_orders": sum.RefundedOrders,
			"refunded_cents":  sum.RefundedCents,
			"txn_cents":       txnSum.TxnCents,
			"grants":          grantCount,
		},
	})
}

// adminOrderDetail is the three-ledger detail for a single order (GET /api/payments/orders/:order_no, OD-2).
func (h *Handler) adminOrderDetail(c *gin.Context) {
	row, ok := h.loadReconcileRow(c.Param("order_no"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, row)
}

// errRefundNotPaid is the sentinel for an unmet refund precondition: only paid orders are refundable.
var errRefundNotPaid = errors.New("order not paid")

// refundOrder is the refund transition (POST /api/payments/orders/:order_no/refund, OD-2):
// only paid -> refunded (unpaid/failed/expired have no money to return, and an
// already-refunded order cannot be refunded again, 409); the panel only does
// the state transition and leaves a trace, the actual money goes back via the
// channel's back office (note records the refund number/reason).
// Entitlements are clawed back with the refund (P2: fix for refund not
// recovering entitlements) -- the quota/duration the order granted is deducted
// inside the same transaction, and a failed clawback rolls back the whole
// refund (the refunded state implies entitlements have been reclaimed).
func (h *Handler) refundOrder(c *gin.Context) {
	var body struct {
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		// note is optional, an empty request body is allowed
		fail(c, http.StatusBadRequest, err)
		return
	}
	now := time.Now()
	err := h.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&storage.PaymentOrder{}).
			Where("order_no = ? AND status = ?", c.Param("order_no"), "paid").
			Updates(map[string]any{
				"status": "refunded", "refund_at": now, "refund_note": body.Note,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errRefundNotPaid
		}
		return revokeOrderGrants(tx, c.Param("order_no"), now)
	})
	if err != nil {
		if errors.Is(err, errRefundNotPaid) {
			fail(c, http.StatusConflict, errors.New("仅已支付订单可退款（或已是退款态）"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	ringlog.Default().Add(fmt.Sprintf("order refund: %s（%s）", c.Param("order_no"), body.Note))
	row, ok := h.loadReconcileRow(c.Param("order_no"))
	if !ok {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	c.JSON(http.StatusOK, row)
}

// revokeOrderGrants reclaims the entitlements an order granted (in the same
// transaction as the refund state transition):
// only the add_quota/extend_days kinds have a reclamation rule, matched
// explicitly -- unknown kinds are skipped without deduction (the old
// implementation's else fallback treated any non-add_quota kind as a
// expiry extension to claw back).
// add_quota deducts from the current balance (floored at 0 -- traffic may
// already have been consumed, no negative balance recorded); extend_days
// deducts the days from the current expiry (landing in the past means the
// expired state; with no current expiry there is nothing to deduct, left
// alone).
// The write-back carries an optimistic guard (WHERE compared against the
// read values); when it interleaves with a concurrent applyGrant
// read-modify-write, it re-reads and recomputes for at most three rounds to
// avoid lost updates (codex deep review P2). Grants whose user row is already
// deleted cannot be reclaimed and are skipped (grant rows have no foreign key
// and do not cascade on user deletion). A site notification informs the user
// of the reclamation (at most one per user).
func revokeOrderGrants(tx *gorm.DB, orderNo string, now time.Time) error {
	var grants []storage.Grant
	if err := tx.Where("order_no = ?", orderNo).Find(&grants).Error; err != nil {
		return err
	}
	notified := map[uint]bool{}
	for _, g := range grants {
		var u storage.User
		if err := tx.First(&u, g.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue // user already deleted: nothing to reclaim
			}
			return err
		}
		if err := revokeOneGrant(tx, g, &u); err != nil {
			return err
		}
		if !notified[u.ID] {
			notified[u.ID] = true
			if err := tx.Create(&storage.Notification{
				UserID: int64(u.ID), Type: storage.NotifSystem,
				Title: "订单已退款，对应权益已回收", CreatedAt: now,
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// revokeOneGrant reclaims a single grant. Nothing to reclaim (unknown kind /
// no expiry) returns nil and skips; the write-back is guarded optimistically
// against the read values, and if the guard misses (someone else updated the
// row concurrently, e.g. an applyGrant at the same moment) it re-reads,
// recomputes, and retries -- if all three rounds still fail it errors and
// rolls back the whole refund transaction: the refunded state implies the
// entitlements were reclaimed, so a failed refund is better than silently
// under-collecting.
func revokeOneGrant(tx *gorm.DB, g storage.Grant, u *storage.User) error {
	for attempt := 0; attempt < 3; attempt++ {
		updates, ok := revokeUpdates(g, u)
		if !ok {
			return nil
		}
		q := tx.Model(&storage.User{}).Where("id = ?", u.ID)
		if _, has := updates["quota_bytes"]; has {
			q = q.Where("quota_bytes = ?", u.QuotaBytes)
		}
		if _, has := updates["expires_at"]; has && u.ExpiresAt != nil {
			q = q.Where("expires_at = ?", *u.ExpiresAt)
		}
		res := q.Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 1 {
			return nil
		}
		if attempt == 2 {
			return errors.New("revoke grant: concurrent user update contention")
		}
		// Guard missed: someone else updated the row concurrently; re-read and
		// recompute from the new values, then retry.
		if err := tx.First(u, u.ID).Error; err != nil {
			return err
		}
	}
	return nil
}

// revokeUpdates computes the reclamation write set per grant kind: ok=false
// means nothing to reclaim (unknown kind / no expiry). The formula depends
// only on the user row read, so it is recomputed on each retry's re-read.
func revokeUpdates(g storage.Grant, u *storage.User) (map[string]any, bool) {
	switch g.GrantType {
	case "add_quota":
		q := u.QuotaBytes - g.GrantValue
		if q < 0 {
			q = 0
		}
		return map[string]any{"quota_bytes": q}, true
	case "extend_days":
		if u.ExpiresAt == nil {
			return nil, false
		}
		revoked := u.ExpiresAt.Add(-time.Duration(g.GrantValue) * 24 * time.Hour)
		return map[string]any{"expires_at": revoked}, true
	default:
		return nil, false
	}
}

// loadReconcileRow assembles the three-ledger view of one order; it returns (nil, false) when the order does not exist.
func (h *Handler) loadReconcileRow(orderNo string) (*reconcileRow, bool) {
	var order storage.PaymentOrder
	if err := h.db.Where("order_no = ?", orderNo).First(&order).Error; err != nil {
		return nil, false
	}
	row := &reconcileRow{PaymentOrder: order}
	_ = h.db.Where("order_no = ?", orderNo).Order("id").Find(&row.Transactions).Error
	_ = h.db.Where("order_no = ?", orderNo).Order("id").Find(&row.Grants).Error
	return row, true
}
