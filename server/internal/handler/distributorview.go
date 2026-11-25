package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/sub"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// Distributor self-service surface (DS-3): read-only views over own
// batches/card keys/customers/orders/usage/settlement. Settlement actions
// live on the admin surface (DS-2); login goes through
// POST /distributor/login (DS-1). The "never stack with a coupon, take the
// better one" rule (§3.3) lands in the redemption path — the coupon system
// is not started, so there is no stacking point yet.

// distAuthMiddleware is the distributor identity middleware (DS-3): it
// parses tokens carrying role=distributor and re-checks the row in the
// database so a missing or disabled distributor is kicked. Admin tokens
// carry no role claim and therefore always get 401 here — the two surfaces
// cannot cross (same network-boundary rule as adminAuthMiddleware).
func (h *Handler) distAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		tokenStr := ""
		if strings.HasPrefix(auth, "Bearer ") {
			tokenStr = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		}
		if tokenStr == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "令牌缺失"})
			c.Abort()
			return
		}
		claims := &distributorClaims{}
		// Same source as DistributorLogin signing: the configured secret
		// (FERRY_ADMIN_SECRET takes effect), falling back to the constant
		// when empty. It used to be a hardcoded constant — once ops set
		// FERRY_ADMIN_SECRET, signing and verification disagreed and the
		// whole distributor surface returned 401.
		secret := h.cfg.AdminSecret
		if secret == "" {
			secret = "ferry-admin-secret"
		}
		tkn, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		})
		if err != nil || !tkn.Valid || claims.Role != "distributor" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "无效令牌"})
			c.Abort()
			return
		}
		id, err := strconv.ParseUint(claims.ID, 10, 32)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "无效令牌"})
			c.Abort()
			return
		}
		// Re-check the row: a deleted or disabled distributor is kicked
		// (disabling also blocks login).
		var d storage.Distributor
		if err := h.db.First(&d, uint(id)).Error; err != nil || !d.Enabled {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "账户不可用"})
			c.Abort()
			return
		}
		c.Set("distID", d.ID)
		c.Next()
	}
}

// distMe returns the distributor's own profile plus the four account
// figures (GET /distributor/api/me).
func (h *Handler) distMe(c *gin.Context) {
	distID := c.GetUint("distID")
	var d storage.Distributor
	if err := h.db.First(&d, distID).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	totals, err := h.distributorTotals()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	t := totals[distID]
	c.JSON(http.StatusOK, gin.H{
		"id": d.ID, "username": d.Username, "discount_percent": d.DiscountPercent,
		"note": d.Note, "enabled": d.Enabled, "created_at": d.CreatedAt,
		"sale_cents": t.Sale, "commission_cents": t.Commission,
		"payout_cents": t.Payout, "balance_cents": t.Balance,
	})
}

// distBatches lists own batches with remaining counts
// (GET /distributor/api/batches).
func (h *Handler) distBatches(c *gin.Context) {
	distID := c.GetUint("distID")
	var batches []storage.CardBatch
	if err := h.db.Where("distributor_id = ?", distID).Order("id DESC").Find(&batches).Error; err != nil {
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
	out := make([]cardBatchView, 0, len(batches))
	for _, b := range batches {
		out = append(out, cardBatchView{CardBatch: b, Remaining: remaining[b.ID]})
	}
	c.JSON(http.StatusOK, out)
}

// distBatchCodes returns the card keys of an own batch
// (GET /distributor/api/batches/:id/codes): a batch that is not ours is
// treated as nonexistent (404, matching the mutual-invisibility rule).
func (h *Handler) distBatchCodes(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var batch storage.CardBatch
	if err := h.db.Where("id = ? AND distributor_id = ?", id, c.GetUint("distID")).First(&batch).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("batch not found"))
		} else {
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	var codes []storage.CardCode
	if err := h.db.Where("batch_id = ?", batch.ID).Order("id").Find(&codes).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, codes)
}

// distCustomerRow is a distributor customer row: redeemed key count plus
// current usage/expiration (read-only). Status/Expired/Limited are the split
// gate state (G3); Active/OverQuota stay for the existing panel consumers.
type distCustomerRow struct {
	ID           uint       `json:"id"`
	Username     string     `json:"username"`
	RedeemedCnt  int64      `json:"redeemed_cnt"` // number of own keys redeemed
	QuotaBytes   int64      `json:"quota_bytes"`
	UsedBytes    int64      `json:"used_bytes"` // accumulated over the cycle window (same rule as the user portal)
	ExpiresAt    *time.Time `json:"expires_at"`
	Active       bool       `json:"active"`
	OverQuota    bool       `json:"over_quota"`
	Status       string     `json:"status"`
	Expired      bool       `json:"expired"`
	Limited      bool       `json:"limited"`
	RedeemedLast *time.Time `json:"redeemed_last"` // time of the most recent redemption
}

// lastAt returns the user's most recent redemption time (nil when there is
// no record).
func lastAt(last map[uint]time.Time, uid uint) *time.Time {
	if t, ok := last[uid]; ok && !t.IsZero() {
		return &t
	}
	return nil
}

// distCustomers lists customers (GET /distributor/api/customers): users who
// redeemed an own-batch key, deduplicated, with redeemed count / last
// redemption / cycle usage (usageQuery, same rule as the portal).
func (h *Handler) distCustomers(c *gin.Context) {
	distID := c.GetUint("distID")
	var batchIDs []uint
	if err := h.db.Model(&storage.CardBatch{}).Where("distributor_id = ?", distID).Pluck("id", &batchIDs).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]distCustomerRow, 0)
	if len(batchIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"customers": out})
		return
	}
	// Aggregate the redeemed key rows in memory for counts and last
	// redemption: a raw MAX(time) aggregate scans unevenly across SQL
	// dialects, and distributor row counts are small enough.
	var redeemed []storage.CardCode
	if err := h.db.Select("used_by", "used_at").
		Where("batch_id IN ? AND used_by > 0", batchIDs).Find(&redeemed).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if len(redeemed) == 0 {
		c.JSON(http.StatusOK, gin.H{"customers": out})
		return
	}
	userIDs := make([]uint, 0, len(redeemed))
	cnt := make(map[uint]int64)
	last := make(map[uint]time.Time)
	for _, rc := range redeemed {
		if rc.UsedBy == nil || *rc.UsedBy == 0 {
			continue
		}
		uid := *rc.UsedBy
		if cnt[uid] == 0 {
			userIDs = append(userIDs, uid)
		}
		cnt[uid]++
		if rc.UsedAt != nil && rc.UsedAt.After(last[uid]) {
			last[uid] = *rc.UsedAt
		}
	}
	var users []storage.User
	if err := h.db.Find(&users, userIDs).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	now := time.Now()
	for _, u := range users {
		var usage struct{ Rx, Tx int64 }
		if err := h.usageQuery(u.ID, u.ResetCycle, now).
			Select("COALESCE(SUM(rx_bytes),0) AS rx, COALESCE(SUM(tx_bytes),0) AS tx").
			Scan(&usage).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		used := usage.Rx + usage.Tx
		st := sub.UserStateOf(&u, used, now)
		out = append(out, distCustomerRow{
			ID: u.ID, Username: u.Username, RedeemedCnt: cnt[u.ID],
			QuotaBytes: u.QuotaBytes, UsedBytes: used, ExpiresAt: u.ExpiresAt,
			Active:       st.Status == sub.StatusActive,
			OverQuota:    st.Limited,
			Status:       st.Status,
			Expired:      st.Expired,
			Limited:      st.Limited,
			RedeemedLast: lastAt(last, u.ID),
		})
	}
	c.JSON(http.StatusOK, gin.H{"customers": out})
}

// distOrders lists own redemption orders (GET /distributor/api/orders):
// card orders carrying our distributor_id, with the customer username
// attached and the amount side 0 (the list price sits on the batch and in
// the ledger).
func (h *Handler) distOrders(c *gin.Context) {
	distID := c.GetUint("distID")
	limit := 50
	if s := c.Query("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	var orders []storage.PaymentOrder
	if err := h.db.Where("distributor_id = ?", distID).Order("id DESC").Limit(limit).Find(&orders).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	type orderRow struct {
		storage.PaymentOrder
		Username string `json:"username"`
	}
	userIDs := make([]uint, 0, len(orders))
	for _, o := range orders {
		userIDs = append(userIDs, o.UserID)
	}
	names := map[uint]string{}
	if len(userIDs) > 0 {
		var users []storage.User
		if err := h.db.Select("id, username").Find(&users, userIDs).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		for _, u := range users {
			names[u.ID] = u.Username
		}
	}
	out := make([]orderRow, 0, len(orders))
	for _, o := range orders {
		out = append(out, orderRow{PaymentOrder: o, Username: names[o.UserID]})
	}
	c.JSON(http.StatusOK, gin.H{"orders": out})
}

// distLedger returns the own account ledger and balance
// (GET /distributor/api/ledger, read-only — settlement actions live on the
// admin surface).
func (h *Handler) distLedger(c *gin.Context) {
	distID := c.GetUint("distID")
	limit := 50
	if s := c.Query("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	var rows []storage.DistributorLedger
	if err := h.db.Where("distributor_id = ?", distID).Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	totals, err := h.distributorTotals()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ledger": rows, "balance_cents": totals[distID].Balance})
}
