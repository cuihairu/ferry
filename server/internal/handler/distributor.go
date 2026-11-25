package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// distributorClaims is the distributor login token (DS distribution): signed with the same key as admin tokens but
// with a different claim shape (role is always distributor) — a distributor token fails admin validation,
// and an admin token fails distributor validation, so the two identities cannot cross into each other.
type distributorClaims struct {
	jwt.RegisteredClaims
	Role string `json:"role"` // always "distributor"
}

// DistributorLogin distributor login (DS-1): distributor credentials independent of users, reusing the
// admin bcrypt hashing contract, reusing the redemption rate limiter for brute-force protection, and issuing a role=distributor JWT (24h).
// The distAuth middleware and the distributor self-view land with DS-3.
func (h *Handler) DistributorLogin(c *gin.Context) {
	ip := c.ClientIP()
	if !h.loginLimiter.Allow(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试过于频繁，请稍后再试"})
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	var d storage.Distributor
	err := h.db.Where("username = ?", strings.TrimSpace(in.Username)).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Not-found and wrong password both return 401, preventing enumeration (same contract as admin login).
		h.loginLimiter.RecordFailure(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "内部错误"})
		return
	}
	if !d.Enabled {
		h.loginLimiter.RecordFailure(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "账户已停用"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(d.PasswordHash), []byte(in.Password)); err != nil {
		h.loginLimiter.RecordFailure(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	h.loginLimiter.Reset(ip)
	now := time.Now()
	claims := distributorClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   d.Username,
			ID:        strconv.FormatUint(uint64(d.ID), 10),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		Role: "distributor",
	}
	secret := h.cfg.AdminSecret
	if secret == "" {
		secret = "ferry-admin-secret"
	}
	ss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "内部错误"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token": ss, "id": d.ID, "username": d.Username, "discount_percent": d.DiscountPercent,
	})
}

// distributorView is a distributor list row: carries the reconciliation quadruple (card-sale revenue / commission / settled / unsettled).
type distributorView struct {
	storage.Distributor
	SaleCents       int64 `json:"sale_cents"`       // Σsale (trace statistics only)
	CommissionCents int64 `json:"commission_cents"` // Σcommission (revenue share)
	PayoutCents     int64 `json:"payout_cents"`     // Σpayout (settled payments)
	BalanceCents    int64 `json:"balance_cents"`    // unsettled = Σcommission + Σadjust − Σpayout
}

// listDistributors distributor list (DS-1): carries the reconciliation quadruple summary, feeding the dash distributor management card and
// the reconciliation view's distributor dimension (DS-2).
func (h *Handler) listDistributors(c *gin.Context) {
	var dists []storage.Distributor
	if err := h.db.Order("id DESC").Find(&dists).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	totals, err := h.distributorTotals()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]distributorView, 0, len(dists))
	for _, d := range dists {
		t := totals[d.ID]
		out = append(out, distributorView{
			Distributor: d, SaleCents: t.Sale, CommissionCents: t.Commission,
			PayoutCents: t.Payout, BalanceCents: t.Balance,
		})
	}
	c.JSON(http.StatusOK, gin.H{"distributors": out})
}

// distributorTotals is the per-distributor reconciliation quadruple (DS-2): sale is only traced and never enters the balance,
// commission/adjust are added directly by sign, and payout is deducted in reverse.
type distributorTotals struct {
	Sale       int64
	Commission int64
	Payout     int64
	Balance    int64
}

func (h *Handler) distributorTotals() (map[uint]distributorTotals, error) {
	var rows []struct {
		DistributorID uint
		Sale          int64
		Commission    int64
		Payout        int64
		Balance       int64
	}
	err := h.db.Model(&storage.DistributorLedger{}).
		Select("distributor_id, " +
			"COALESCE(SUM(CASE WHEN kind = 'sale' THEN amount_cents ELSE 0 END), 0) AS sale, " +
			"COALESCE(SUM(CASE WHEN kind = 'commission' THEN amount_cents ELSE 0 END), 0) AS commission, " +
			"COALESCE(SUM(CASE WHEN kind = 'payout' THEN amount_cents ELSE 0 END), 0) AS payout, " +
			"COALESCE(SUM(CASE WHEN kind IN ('commission','adjust') THEN amount_cents " +
			"WHEN kind = 'payout' THEN -amount_cents ELSE 0 END), 0) AS balance").
		Group("distributor_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]distributorTotals, len(rows))
	for _, r := range rows {
		out[r.DistributorID] = distributorTotals{Sale: r.Sale, Commission: r.Commission, Payout: r.Payout, Balance: r.Balance}
	}
	return out, nil
}

// distributorInput is the distributor create/update payload: password is required on create and optional on update (empty leaves it untouched).
type distributorInput struct {
	Username        string  `json:"username"`
	Password        string  `json:"password"`
	DiscountPercent *int    `json:"discount_percent"`
	Note            *string `json:"note"`
	Enabled         *bool   `json:"enabled"`
}

// createDistributor creates a distributor account (DS-1): unique username, commission ratio 0-100,
// and no delete offered — disabling blocks login while keeping the accounts intact.
func (h *Handler) createDistributor(c *gin.Context) {
	var in distributorInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	username := strings.TrimSpace(in.Username)
	if username == "" || len(username) > 64 {
		fail(c, http.StatusBadRequest, errors.New("username is required (1-64 chars)"))
		return
	}
	if in.Password == "" {
		fail(c, http.StatusBadRequest, errors.New("password is required"))
		return
	}
	dp := 0
	if in.DiscountPercent != nil {
		dp = *in.DiscountPercent
	}
	if dp < 0 || dp > 100 {
		fail(c, http.StatusBadRequest, errors.New("discount_percent must be 0-100"))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	note := ""
	if in.Note != nil {
		note = strings.TrimSpace(*in.Note)
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	d := storage.Distributor{
		Username: username, PasswordHash: string(hash),
		DiscountPercent: dp, Note: note, Enabled: enabled,
	}
	if err := h.db.Create(&d).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("username already exists"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !enabled {
		// Enabled carries default:true, so the zero value false is skipped by GORM and the DB default is persisted; it needs an explicit write-back.
		// After Create the struct is refilled with the DB default true (RETURNING), so decide from the input parameter.
		if err := h.db.Model(&storage.Distributor{}).Where("id=?", d.ID).Update("enabled", false).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		d.Enabled = false
	}
	c.JSON(http.StatusCreated, gin.H{"distributor": d})
}

// updateDistributor partial update (DS-1): password reset / commission ratio / enabled switch / note;
// the username cannot change (the login identity is the account subject), and no delete is offered.
func (h *Handler) updateDistributor(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in distributorInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var d storage.Distributor
	if err := h.db.First(&d, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("distributor not found"))
		} else {
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	updates := map[string]any{}
	if in.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		updates["password_hash"] = string(hash)
	}
	if in.DiscountPercent != nil {
		if *in.DiscountPercent < 0 || *in.DiscountPercent > 100 {
			fail(c, http.StatusBadRequest, errors.New("discount_percent must be 0-100"))
			return
		}
		updates["discount_percent"] = *in.DiscountPercent
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if in.Note != nil {
		updates["note"] = strings.TrimSpace(*in.Note)
	}
	if len(updates) == 0 {
		fail(c, http.StatusBadRequest, errors.New("no fields to update"))
		return
	}
	updates["updated_at"] = time.Now()
	if err := h.db.Model(&storage.Distributor{}).Where("id = ?", d.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.First(&d, d.ID).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"distributor": d})
}

// distributorLedger distributor account ledger (DS-1): id DESC paging, with the unsettled balance attached.
func (h *Handler) distributorLedger(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var d storage.Distributor
	if err := h.db.First(&d, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("distributor not found"))
		} else {
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	limit := 50
	if s := c.Query("limit"); s != "" {
		if n, perr := strconv.Atoi(s); perr == nil {
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
	if err := h.db.Where("distributor_id = ?", id).Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	totals, err := h.distributorTotals()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ledger": rows, "balance_cents": totals[uint(id)].Balance})
}

// distributorPayout settles a payout (DS-2): writes a payout row and deducts the unsettled balance accordingly.
// Payout is a manual offline action (settlement terms agreed offline; the panel only books entries and never pays automatically); the amount must not
// exceed the balance — overpaying first posts an adjust entry, keeping the balance domain non-negative.
func (h *Handler) distributorPayout(c *gin.Context) {
	h.writeLedgerOp(c, "payout")
}

// distributorAdjust manual adjustment (DS-2): ±amount for correction/compensation; a zero amount is meaningless and rejected.
func (h *Handler) distributorAdjust(c *gin.Context) {
	h.writeLedgerOp(c, "adjust")
}

// writeLedgerOp handles the two kinds of manual account operations, payout/adjust: validates distributor existence, amount
// semantics and balance constraints, writes one ledger row and returns the new balance. The payout balance constraint lives in the booking
// statement itself (conditional INSERT, see below); the precheck is only a fast failure and does not provide concurrency safety.
func (h *Handler) writeLedgerOp(c *gin.Context, kind string) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var d storage.Distributor
	if err := h.db.First(&d, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("distributor not found"))
		} else {
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	var in struct {
		AmountCents int64  `json:"amount_cents"`
		Note        string `json:"note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if kind == "payout" && in.AmountCents <= 0 {
		fail(c, http.StatusBadRequest, errors.New("amount_cents must be > 0"))
		return
	}
	if kind == "adjust" && in.AmountCents == 0 {
		fail(c, http.StatusBadRequest, errors.New("amount_cents must be nonzero"))
		return
	}
	totals, err := h.distributorTotals()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	balance := totals[uint(id)].Balance
	if kind == "payout" && in.AmountCents > balance {
		fail(c, http.StatusBadRequest, errors.New("payout exceeds unsettled balance"))
		return
	}
	row := storage.DistributorLedger{
		DistributorID: uint(id), Kind: kind, AmountCents: in.AmountCents,
		Note: strings.TrimSpace(in.Note), CreatedAt: time.Now(),
	}
	if kind == "payout" {
		// Booking a payout does validation and write-up in one conditional INSERT: concurrent payouts all pass the
		// fast-read precheck above, but only the one whose aggregate balance is still sufficient actually books; a 0-row insert
		// means overdraft was refused. The balance contract matches distributorTotals (commission+adjust−payout);
		// FROM (SELECT 1) is MySQL's placeholder preceding WHERE (three-dialect compatibility).
		res := h.db.Exec(
			`INSERT INTO distributor_ledgers
				(distributor_id, kind, amount_cents, note, created_at)
			 SELECT ?, 'payout', ?, ?, ? FROM (SELECT 1) AS t
			 WHERE (SELECT COALESCE(SUM(CASE WHEN kind IN ('commission','adjust') THEN amount_cents
				 WHEN kind = 'payout' THEN -amount_cents ELSE 0 END), 0)
				 FROM distributor_ledgers WHERE distributor_id = ?) >= ?`,
			uint(id), in.AmountCents, row.Note, row.CreatedAt, uint(id), in.AmountCents)
		if res.Error != nil {
			fail(c, http.StatusInternalServerError, res.Error)
			return
		}
		if res.RowsAffected == 0 {
			fail(c, http.StatusBadRequest, errors.New("payout exceeds unsettled balance"))
			return
		}
	} else if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	newBalance := balance + in.AmountCents
	if kind == "payout" {
		newBalance = balance - in.AmountCents
	}
	c.JSON(http.StatusCreated, gin.H{"ledger": row, "balance_cents": newBalance})
}
