package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
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
	// 缺失环节：paid 无流水、paid 带发放口径无发放、pending 却有流水。
	var paidOrders, paidCents, txnCents, grantCount int64
	rows := make([]reconcileRow, 0, len(orders))
	for _, o := range orders {
		row := byNo[o.OrderNo]
		if o.Status == "paid" {
			paidOrders++
			paidCents += o.AmountCents
			if len(row.Transactions) == 0 {
				row.Missing = append(row.Missing, "缺支付流水")
			}
			if o.GrantType != "" && len(row.Grants) == 0 {
				row.Missing = append(row.Missing, "缺发放")
			}
		}
		if o.Status == "pending" && len(row.Transactions) > 0 {
			row.Missing = append(row.Missing, "有流水未结算")
		}
		for _, t := range row.Transactions {
			txnCents += t.AmountCents
		}
		grantCount += int64(len(row.Grants))
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

	c.JSON(http.StatusOK, gin.H{
		"orders":  rows,
		"orphans": orphans,
		"summary": gin.H{
			"paid_orders": paidOrders,
			"paid_cents":  paidCents,
			"txn_cents":   txnCents,
			"grants":      grantCount,
		},
	})
}
