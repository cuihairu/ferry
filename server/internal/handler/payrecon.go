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

// fmtOrphanAmount 是游离流水的摘要文案。
func fmtOrphanAmount(cents int64) string {
	return fmt.Sprintf("%d 分", cents)
}

// 三账对账（PAY-9）：订单/流水/发放按订单分组返回，标出缺失环节，
// 另附游离记录（无对应订单）与总额小计，供 dash 对账视图。

// reconcileRow 是一个订单的三账视图：挂在 order 上聚合其流水与发放，
// missing 列出对不上的环节文案。
type reconcileRow struct {
	storage.PaymentOrder
	Transactions []storage.PaymentTransaction `json:"transactions"`
	Grants       []storage.Grant              `json:"grants"`
	Missing      []string                     `json:"missing"`
}

// reconcileOrphan 是游离记录：有流水/发放却找不到订单。
type reconcileOrphan struct {
	Kind     string `json:"kind"` // transaction / grant
	OrderNo  string `json:"order_no"`
	Provider string `json:"provider"`
	Detail   string `json:"detail"`
}

// listReconcile 返回三账对账数据（GET /api/payments/reconcile?limit=）。
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
	// 缺失环节：paid/refunded 无流水、paid 带发放口径无发放、pending 却有流水。
	// 三账完整性检查与 paid 同口径（退过款的订单更应有流水与发放可查）。
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

	// 游离记录：流水/发放指向的订单不存在（对账硬伤，单独列出）。
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

	// 小计全库口径（P2：原实现把小计累加在当前页 limit 条上，dash 把它当
	// 总营收展示——订单超过一页后小计静默缩水）。页内行仍按 limit 返回，
	// 小计改由全表聚合：实收/退款按状态分桶，流水与发放为全表合计。
	var sum struct {
		PaidOrders     int64
		PaidCents      int64
		RefundedOrders int64
		RefundedCents  int64
	}
	if err := h.db.Model(&storage.PaymentOrder{}).Select(
		"COALESCE(SUM(CASE WHEN status = 'paid' THEN 1 ELSE 0 END), 0) AS paid_orders, "+
			"COALESCE(SUM(CASE WHEN status = 'paid' THEN amount_cents ELSE 0 END), 0) AS paid_cents, "+
			"COALESCE(SUM(CASE WHEN status = 'refunded' THEN 1 ELSE 0 END), 0) AS refunded_orders, "+
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

// adminOrderDetail 单笔订单三账详情（GET /api/payments/orders/:order_no，OD-2）。
func (h *Handler) adminOrderDetail(c *gin.Context) {
	row, ok := h.loadReconcileRow(c.Param("order_no"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, row)
}

// errRefundNotPaid 是退款前置不满足的哨兵：仅 paid 可退。
var errRefundNotPaid = errors.New("order not paid")

// refundOrder 退款流转（POST /api/payments/orders/:order_no/refund，OD-2）：
// 仅 paid → refunded（未付/失败/过期无款可退，已退款不可再退，409）；
// 面板只做状态流转与留痕，钱款退回经渠道后台操作（note 记退款单号/原因）。
// 权益随退款回收（P2：refund 不回收权益修复）——订单发放过的配额/时长在
// 同一事务内扣回，回收失败整体回滚退款（refunded 态蕴含权益已收）。
func (h *Handler) refundOrder(c *gin.Context) {
	var body struct {
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		// note 选填，允许空请求体
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

// revokeOrderGrants 回收订单发放过的权益（与退款状态流转同事务）：
// add_quota 从当前余额扣回（下限 0——流量可能已消耗，不记负数）；extend_days
// 从当前到期扣回天数（扣后落在过去即过期态；当前无到期无从扣回，不动）。
// 用户行已删的发放无从回收，跳过（发放记录无外键不随用户删除）。回收落一条
// 站内信告知用户（每用户至多一条）。
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
				continue // 用户已删：无从回收
			}
			return err
		}
		if g.GrantType == "add_quota" {
			u.QuotaBytes -= g.GrantValue
			if u.QuotaBytes < 0 {
				u.QuotaBytes = 0
			}
		} else if u.ExpiresAt != nil {
			revoked := u.ExpiresAt.Add(-time.Duration(g.GrantValue) * 24 * time.Hour)
			u.ExpiresAt = &revoked
		} else {
			continue
		}
		if err := tx.Model(&storage.User{}).Where("id = ?", u.ID).
			Updates(map[string]any{"quota_bytes": u.QuotaBytes, "expires_at": u.ExpiresAt}).Error; err != nil {
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

// loadReconcileRow 组装一单的三账视图；订单不存在返回 (nil, false)。
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
