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

// distributorClaims 是代理登录令牌（DS 分销）：与管理员令牌同密钥签发但
// claim 形状不同（role 恒为 distributor）——代理令牌过不了管理员校验，
// 管理员令牌也过不了代理校验，两种身份互不越界。
type distributorClaims struct {
	jwt.RegisteredClaims
	Role string `json:"role"` // 恒 "distributor"
}

// DistributorLogin 代理登录（DS-1）：独立于 users 的代理凭证，bcrypt 复用
// 管理员哈希口径，防爆破复用兑换限速，签发 role=distributor 的 JWT（24h）。
// distAuth 中间件与代理自面视图随 DS-3 落地。
func (h *Handler) DistributorLogin(c *gin.Context) {
	ip := c.ClientIP()
	if !h.redeemLimiter.Allow(ip) {
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
		// 不存在与密码错同回 401，防枚举（与管理员登录同口径）。
		h.redeemLimiter.RecordFailure(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "内部错误"})
		return
	}
	if !d.Enabled {
		h.redeemLimiter.RecordFailure(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "账户已停用"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(d.PasswordHash), []byte(in.Password)); err != nil {
		h.redeemLimiter.RecordFailure(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	h.redeemLimiter.Reset(ip)
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

// distributorView 是代理列表行：附未结算余额。
type distributorView struct {
	storage.Distributor
	BalanceCents int64 `json:"balance_cents"` // Σcommission + Σadjust − Σpayout
}

// listDistributors 代理列表（DS-1）：附未结算余额，供 dash 建号/停用与对账。
func (h *Handler) listDistributors(c *gin.Context) {
	var dists []storage.Distributor
	if err := h.db.Order("id DESC").Find(&dists).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	balances, err := h.distributorBalances()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]distributorView, 0, len(dists))
	for _, d := range dists {
		out = append(out, distributorView{Distributor: d, BalanceCents: balances[d.ID]})
	}
	c.JSON(http.StatusOK, gin.H{"distributors": out})
}

// distributorBalances 按代理汇总未结算余额：sale 只留痕不进余额，
// commission/adjust 按正负直加，payout 反向扣减。
func (h *Handler) distributorBalances() (map[uint]int64, error) {
	var rows []struct {
		DistributorID uint
		Balance       int64
	}
	err := h.db.Model(&storage.DistributorLedger{}).
		Select("distributor_id, COALESCE(SUM(CASE WHEN kind IN ('commission','adjust') THEN amount_cents " +
			"WHEN kind = 'payout' THEN -amount_cents ELSE 0 END), 0) AS balance").
		Group("distributor_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]int64, len(rows))
	for _, r := range rows {
		out[r.DistributorID] = r.Balance
	}
	return out, nil
}

// distributorInput 是代理建号/更新的载荷：密码建号必填、更新可选（留空不动）。
type distributorInput struct {
	Username        string  `json:"username"`
	Password        string  `json:"password"`
	DiscountPercent *int    `json:"discount_percent"`
	Note            *string `json:"note"`
	Enabled         *bool   `json:"enabled"`
}

// createDistributor 建代理号（DS-1）：用户名唯一，佣金比例 0-100，
// 不提供删除——停用即禁登录，账目保留。
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
	d := storage.Distributor{
		Username: username, PasswordHash: string(hash),
		DiscountPercent: dp, Note: note, Enabled: true,
	}
	if in.Enabled != nil {
		d.Enabled = *in.Enabled
	}
	if err := h.db.Create(&d).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("username already exists"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !d.Enabled {
		// Enabled 带 default:true，零值 false 会被 GORM 略过落库默认值，需显式补写。
		if err := h.db.Model(&storage.Distributor{}).Where("id=?", d.ID).Update("enabled", false).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.JSON(http.StatusCreated, gin.H{"distributor": d})
}

// updateDistributor 部分更新（DS-1）：密码重置/佣金比例/启用开关/备注；
// 用户名不可改（登录身份即账目主体），不提供删除。
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

// distributorLedger 代理账目流水（DS-1）：id DESC 分页，附未结算余额；
// payout 结算与 adjust 人工调随 DS-2 管理面接生产点。
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
	balances, err := h.distributorBalances()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ledger": rows, "balance_cents": balances[uint(id)]})
}
