package handler

import (
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Payment-result compensation (F-1307): the payrecon view flags paid orders
// whose grant leg is missing ("缺发放"); until now the flag had no remedy.
// CompensateGrants is the remedy: it re-applies the entitlement for every
// paid order carrying a grant spec with zero Grant rows. The guard is the
// grant count inside the same transaction as applyGrant, so a row is granted
// at most once even when two compensations race (serialized DB writes make
// the count-then-apply pair atomic in effect). Refunded orders are never
// selected: their grants, if any, were reclaimed with the refund.
//
// The other payrecon fault classes are deliberately out of this mechanism's
// reach: money-arrived-but-unsettled rows (expired/pending with transactions)
// stay a review flag by design -- settling them would break the F-1303 rule
// that only pending orders settle -- and provider-side result re-query
// (pulling the upstream status instead of waiting for callbacks) needs a
// Provider interface extension no registered channel implements.
type compensateResult struct {
	OrderNo string `json:"order_no"`
	Action  string `json:"action"` // granted / skipped
	Detail  string `json:"detail,omitempty"`
}

// CompensateGrants re-issues missing grants and returns the per-order report;
// a per-order failure lands in the report and never aborts the rest.
func CompensateGrants(db *gorm.DB, now time.Time) ([]compensateResult, int, error) {
	var orders []storage.PaymentOrder
	if err := db.Where("status = ? AND grant_type <> ? AND order_no NOT IN (?)",
		"paid", "", db.Model(&storage.Grant{}).Select("order_no")).
		Order("id").Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	results := make([]compensateResult, 0, len(orders))
	granted := 0
	for _, o := range orders {
		action := "skipped"
		detail := ""
		err := db.Transaction(func(tx *gorm.DB) error {
			var cnt int64
			if err := tx.Model(&storage.Grant{}).Where("order_no = ?", o.OrderNo).Count(&cnt).Error; err != nil {
				return err
			}
			if cnt > 0 {
				detail = "grant already present"
				return nil
			}
			var u storage.User
			if err := tx.First(&u, o.UserID).Error; err != nil {
				return err
			}
			return applyGrant(tx, o.OrderNo, &u, o.GrantType, o.GrantValue, now,
				map[string]any{"source": "compensate"})
		})
		if err != nil {
			action = "failed"
			detail = err.Error()
		} else if detail == "" {
			action = "granted"
			granted++
		}
		results = append(results, compensateResult{OrderNo: o.OrderNo, Action: action, Detail: detail})
	}
	return results, granted, nil
}

// compensateGrants handles POST /api/payments/compensate (F-1307): run the
// missing-grant compensation on demand (the operator's remedy after reviewing
// the payrecon flags) and return the per-order report.
func (h *Handler) compensateGrants(c *gin.Context) {
	results, granted, err := CompensateGrants(h.db, time.Now())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if results == nil {
		results = []compensateResult{}
	}
	c.JSON(http.StatusOK, gin.H{"results": results, "granted": granted})
}
