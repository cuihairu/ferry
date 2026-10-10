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

// 代理自面（DS-3）：只读视图——自有批次/卡密/客户/订单/用量/结算，
// 结算动作在管理员面（DS-2）；登录走 POST /distributor/login（DS-1）。
// 与优惠码不叠加取优（§3.3）在兑换链路落地——优惠体系未开工，现无叠加点。

// distAuthMiddleware 代理身份中间件（DS-3）：解析 role=distributor 令牌，
// 回库校验代理存在且启用（停用即踢）。管理员令牌 claim 无 role 必 401，
// 与管理面令牌互不越界（同 adminAuthMiddleware 网络边界口径）。
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
		// 与 DistributorLogin 签发同源：cfg 秘钥（FERRY_ADMIN_SECRET 生效），
		// 空回落常量。此前硬编码常量——运营设了 FERRY_ADMIN_SECRET 后
		// 签发与验签秘钥错位，代理自面整面 401。
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
		// 回库校验：代理被删/停用即踢（停用即禁登录口径）。
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

// distMe 代理自身资料与账目四元组（GET /distributor/api/me）。
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

// distBatches 自有批次列表含剩余（GET /distributor/api/batches）。
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

// distBatchCodes 自有批次卡密（GET /distributor/api/batches/:id/codes）：
// 批次不属于自己视同不存在（404，与代理互不可见口径一致）。
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

// distCustomerRow 是代理客户行：兑换张数与当前用量/到期（只读）。
type distCustomerRow struct {
	ID           uint       `json:"id"`
	Username     string     `json:"username"`
	RedeemedCnt  int64      `json:"redeemed_cnt"` // 兑换自有卡张数
	QuotaBytes   int64      `json:"quota_bytes"`
	UsedBytes    int64      `json:"used_bytes"` // 周期窗口累计（与用户门户同口径）
	ExpiresAt    *time.Time `json:"expires_at"`
	Active       bool       `json:"active"`
	OverQuota    bool       `json:"over_quota"`
	RedeemedLast *time.Time `json:"redeemed_last"` // 最近一次兑换时间
}

// lastAt 取用户最近兑换时间（无记录回 nil）。
func lastAt(last map[uint]time.Time, uid uint) *time.Time {
	if t, ok := last[uid]; ok && !t.IsZero() {
		return &t
	}
	return nil
}

// distCustomers 客户列表（GET /distributor/api/customers）：兑换过自有批次
// 卡密的用户去重，附兑换张数/最近兑换/周期用量（usageQuery 同门户口径）。
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
	// 已兑换卡行进内存聚合张数与最近兑换（裸聚合 MAX(time) 跨方言扫描不稳，
	// 代理场景行数可控）。
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
		out = append(out, distCustomerRow{
			ID: u.ID, Username: u.Username, RedeemedCnt: cnt[u.ID],
			QuotaBytes: u.QuotaBytes, UsedBytes: used, ExpiresAt: u.ExpiresAt,
			Active:       sub.UserActive(&u, used, now),
			OverQuota:    u.QuotaBytes > 0 && used >= u.QuotaBytes,
			RedeemedLast: lastAt(last, u.ID),
		})
	}
	c.JSON(http.StatusOK, gin.H{"customers": out})
}

// distOrders 自有兑换订单（GET /distributor/api/orders）：distributor_id 带
// 归属的 card 单，附客户用户名，金额面=0（面价在批次与账目，见 ledger）。
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

// distLedger 自有账目流水与余额（GET /distributor/api/ledger，只读——
// 结算动作在管理员面）。
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
