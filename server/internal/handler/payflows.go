package handler

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Unified account flow (F-1324): the three ledgers (money transactions,
// entitlement grants, distributor commission ledger) stay physically separate
// -- their semantics differ (money vs entitlement vs commission) and merging
// them into one table would blur the reconciliation guarantees -- but the
// audit view reads them through one typed envelope: every flow carries a
// kind, the order_no tying it to the three-ledger view, a reference string
// (channel+external id / grant kind / distributor id) and one timestamp.
// Amount is money in cents (grants carry 0; their payload lives in Ref).
type accountFlow struct {
	Kind        string    `json:"kind"` // transaction / grant / sale / commission / payout / adjust
	OrderNo     string    `json:"order_no"`
	Ref         string    `json:"ref,omitempty"`
	UserID      uint      `json:"user_id"`
	AmountCents int64     `json:"amount_cents"`
	At          time.Time `json:"at"`
}

// listFlows handles GET /api/payments/flows?limit=N (default 200, cap 500):
// the newest N flows across all three ledgers, merged newest-first. Each
// ledger is read with the same limit before the merge, so the window is
// fair to every kind even when one ledger is hot.
func (h *Handler) listFlows(c *gin.Context) {
	limit := 200
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-500"))
			return
		}
		limit = n
	}
	flows := make([]accountFlow, 0, limit*3)

	var txns []storage.PaymentTransaction
	if err := h.db.Order("id DESC").Limit(limit).Find(&txns).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	for _, t := range txns {
		uid := uint(0)
		var o storage.PaymentOrder
		if err := h.db.Select("user_id").Where("order_no = ?", t.OrderNo).First(&o).Error; err == nil {
			uid = o.UserID
		}
		flows = append(flows, accountFlow{
			Kind: "transaction", OrderNo: t.OrderNo,
			Ref:         t.Provider + "/" + t.ExternalID,
			UserID:      uid,
			AmountCents: t.AmountCents,
			At:          t.OccurredAt,
		})
	}

	var grants []storage.Grant
	if err := h.db.Order("id DESC").Limit(limit).Find(&grants).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	for _, g := range grants {
		flows = append(flows, accountFlow{
			Kind: "grant", OrderNo: g.OrderNo,
			Ref:    g.GrantType + ":" + formatGrantValue(g.GrantValue),
			UserID: g.UserID,
			At:     g.CreatedAt,
		})
	}

	var ledger []storage.DistributorLedger
	if err := h.db.Order("id DESC").Limit(limit).Find(&ledger).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	for _, l := range ledger {
		flows = append(flows, accountFlow{
			Kind: l.Kind, OrderNo: l.OrderNo,
			Ref:         "distributor/" + strconv.FormatUint(uint64(l.DistributorID), 10),
			AmountCents: l.AmountCents,
			At:          l.CreatedAt,
		})
	}

	sort.Slice(flows, func(i, j int) bool {
		if !flows[i].At.Equal(flows[j].At) {
			return flows[i].At.After(flows[j].At)
		}
		return flows[i].OrderNo < flows[j].OrderNo
	})
	if len(flows) > limit {
		flows = flows[:limit]
	}
	c.JSON(http.StatusOK, gin.H{"flows": flows})
}

// formatGrantValue renders a grant payload for the flow Ref: bytes in a
// compact MiB form for quota, plain days otherwise.
func formatGrantValue(v int64) string {
	if v >= 1<<20 {
		return strconv.FormatInt(v>>20, 10) + "MiB"
	}
	return strconv.FormatInt(v, 10)
}
