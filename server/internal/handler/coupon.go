package handler

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Coupons (PROMO-1, ops design §5): admin CRUD + atomic redemption on panel orders.
// Discount selection (DS §3.3): one discount source per order, no stacking — an order applies at most one code, and the
// redemption path has no payment (list price lives in the batch) so there is no stacking point; commission rules unchanged (online orders book no commission, redemptions use list price).

// couponInput is the create/edit payload (pointers signal partial updates).
type couponInput struct {
	Code      string     `json:"code"`
	Kind      string     `json:"kind"`
	Value     int64      `json:"value"`
	Scope     string     `json:"scope"`
	MinAmount int64      `json:"min_amount"`
	StartsAt  *time.Time `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	Total     int64      `json:"total"`
	PerUser   int64      `json:"per_user"`
}

// listCoupons lists coupons (GET /api/coupons): id DESC, dash manages the full set.
func (h *Handler) listCoupons(c *gin.Context) {
	var rows []storage.Coupon
	if err := h.db.Order("id DESC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// validateCouponInput validates the payload and returns an error: kind enum, pct basis points 1-9999,
// cut>0, threshold>=0, total/per_user>=0, scope shape and batch existence.
func (h *Handler) validateCouponInput(in *couponInput) error {
	scope, err := h.validateDiscountRules(in.Kind, in.Value, in.Scope, in.MinAmount)
	if err != nil {
		return err
	}
	in.Scope = scope
	if in.MinAmount < 0 || in.Total < 0 || in.PerUser < 0 {
		return errors.New("min_amount/total/per_user must be >= 0")
	}
	if in.StartsAt != nil && in.EndsAt != nil && in.EndsAt.Before(*in.StartsAt) {
		return errors.New("ends_at must be after starts_at")
	}
	return nil
}

// validateDiscountRules validates the discount rule quadruple (shared contract between coupons and campaign rules):
// kind enum, pct basis points 1-9999, cut>0, threshold>=0, scope shape and batch existence.
// Returns the normalized scope (empty → all).
func (h *Handler) validateDiscountRules(kind string, value int64, scope string, minAmount int64) (string, error) {
	if kind != "cut" && kind != "pct" {
		return "", errors.New("kind must be cut or pct")
	}
	if kind == "pct" {
		if value < 1 || value > 9999 {
			return "", errors.New("pct value must be 1-9999 basis points")
		}
	} else if value <= 0 {
		return "", errors.New("cut value must be > 0")
	}
	if minAmount < 0 {
		return "", errors.New("min_amount must be >= 0")
	}
	if scope == "" {
		scope = "all"
	}
	if scope != "all" {
		id, err := parseBatchScope(scope)
		if err != nil {
			return "", errors.New("scope must be all or batch:<id>")
		}
		var cnt int64
		if err := h.db.Model(&storage.CardBatch{}).Where("id = ?", id).Count(&cnt).Error; err != nil {
			return "", err
		}
		if cnt == 0 {
			return "", fmt.Errorf("batch %d not found", id)
		}
	}
	return scope, nil
}

// parseBatchScope parses the batch:<id> form.
func parseBatchScope(scope string) (uint, error) {
	if !strings.HasPrefix(scope, "batch:") {
		return 0, errors.New("bad scope")
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(scope, "batch:"), 10, 32)
	if err != nil || id == 0 {
		return 0, errors.New("bad scope id")
	}
	return uint(id), nil
}

// createCoupon creates a coupon (POST /api/coupons): code is auto-generated when omitted (16 chars of
// uppercase digits, FERRY-XXXX form matching card-key readability), duplicate code → 409.
func (h *Handler) createCoupon(c *gin.Context) {
	var in couponInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := h.validateCouponInput(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	code := strings.TrimSpace(strings.ToUpper(in.Code))
	if code == "" {
		code = "FERRY-" + randomCode8()
	}
	if in.PerUser == 0 {
		in.PerUser = 1 // design default: 1 use per user
	}
	row := storage.Coupon{
		Code: code, Kind: in.Kind, Value: in.Value, Scope: in.Scope,
		MinAmount: in.MinAmount, StartsAt: in.StartsAt, EndsAt: in.EndsAt,
		Total: in.Total, PerUser: in.PerUser, CreatedAt: time.Now(),
	}
	if err := h.db.Create(&row).Error; err != nil {
		// With TranslateError enabled, ErrDuplicatedKey is detectable; the string fallback covers dialect differences.
		if errors.Is(err, gorm.ErrDuplicatedKey) ||
			strings.Contains(strings.ToLower(err.Error()), "unique") ||
			strings.Contains(strings.ToLower(err.Error()), "duplicated") {
			fail(c, http.StatusConflict, errors.New("code already exists"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, row)
}

// updateCoupon edits a coupon (PUT /api/coupons/:id): full update (except used, read-only);
// setting ends_at to now disables it. No password-class sensitive surface — the coupon itself is echoed back.
func (h *Handler) updateCoupon(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var row storage.Coupon
	if err := h.db.First(&row, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("coupon not found"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var in couponInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := h.validateCouponInput(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.PerUser == 0 {
		in.PerUser = 1
	}
	row.Kind, row.Value, row.Scope, row.MinAmount = in.Kind, in.Value, in.Scope, in.MinAmount
	row.StartsAt, row.EndsAt, row.Total, row.PerUser = in.StartsAt, in.EndsAt, in.Total, in.PerUser
	if code := strings.TrimSpace(strings.ToUpper(in.Code)); code != "" {
		// Duplicate check on code change: pointing at another row is a conflict.
		var dup int64
		h.db.Model(&storage.Coupon{}).Where("code = ? AND id != ?", code, row.ID).Count(&dup)
		if dup > 0 {
			fail(c, http.StatusConflict, errors.New("code already exists"))
			return
		}
		row.Code = code
	}
	if err := h.db.Save(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// deleteCoupon deletes a coupon (DELETE /api/coupons/:id): snapshots of already redeemed
// orders keep their records unaffected (promo_snapshot is self-contained).
func (h *Handler) deleteCoupon(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	res := h.db.Delete(&storage.Coupon{}, uint(id))
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, errors.New("coupon not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// randomCode8 takes an 8-char uppercase alphanumeric random segment (used for coupon default generation).
func randomCode8() string {
	const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%08X", time.Now().UnixNano())
	}
	b := make([]byte, 8)
	for i := range b {
		b[i] = digits[int(buf[i])%len(digits)]
	}
	return string(b)
}

// couponDiscount computes the payable amount after discount (PROMO-1): cut floors at 0; pct basis points round down.
func couponDiscount(kind string, value, price int64) int64 {
	if kind == "pct" {
		return price - price*value/10000
	}
	d := price - value
	if d < 0 {
		return 0
	}
	return d
}

// couponCheck loads and validates a coupon (read-only, no redemption): existence / window / usage cap /
// applicable scope / threshold / per-user cap checked item by item; failures return business-copy errors (readable by panel).
// The order flow's two-phase precheck and the in-transaction redemption share this function to keep the contract consistent.
func couponCheck(tx *gorm.DB, code string, u *storage.User, batch *storage.CardBatch, price int64, now time.Time) (storage.Coupon, error) {
	var cp storage.Coupon
	if err := tx.Where("code = ?", strings.ToUpper(strings.TrimSpace(code))).First(&cp).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return cp, errors.New("优惠码无效")
		}
		return cp, err
	}
	if cp.StartsAt != nil && cp.StartsAt.After(now) {
		return cp, errors.New("优惠码未到使用时间")
	}
	if cp.EndsAt != nil && !cp.EndsAt.After(now) {
		return cp, errors.New("优惠码已过期")
	}
	if cp.Total > 0 && cp.Used >= cp.Total {
		return cp, errors.New("优惠码已领完")
	}
	if cp.Scope != "all" {
		id, err := parseBatchScope(cp.Scope)
		if err != nil || id != batch.ID {
			return cp, errors.New("优惠码不适用于该商品")
		}
	}
	if cp.MinAmount > 0 && price < cp.MinAmount {
		return cp, errors.New("未达优惠码使用门槛")
	}
	// The per-user cap only counts orders that actually redeemed the coupon (P2: fix for pending occupying the cap —
	// an order is set to pending at creation, and if pending counted too, a single unpaid order would permanently
	// consume the user's per-user cap (this repo has no order expiry sweeper; pending rows live forever); changed to
	// count only settled/refunded states that actually consumed the coupon). failed/expired,
	// like pending, are not counted.
	var mine int64
	if err := tx.Model(&storage.PaymentOrder{}).
		Where("user_id = ? AND promo_code = ? AND status IN ?", u.ID, cp.Code, []string{"paid", "refunded"}).
		Count(&mine).Error; err != nil {
		return cp, err
	}
	if cp.PerUser > 0 && mine >= cp.PerUser {
		return cp, errors.New("优惠码每用户限用一次")
	}
	return cp, nil
}

// applyCouponTx atomically redeems a coupon inside the order transaction (PROMO-1): after couponCheck's
// read-only validation passes, a conditional UPDATE used+1 guards against concurrent over-redemption (total=0 means unlimited).
// Returns the payable amount and the code row; on redemption failure the caller rolls back the whole transaction
// (the order row shares the transaction — code and order live and die together).
func applyCouponTx(tx *gorm.DB, code string, u *storage.User, batch *storage.CardBatch, price int64, now time.Time) (int64, storage.Coupon, error) {
	cp, err := couponCheck(tx, code, u, batch, price, now)
	if err != nil {
		return 0, cp, err
	}
	// Conditional-update redemption: concurrency backstop for the total cap (used judged from this row's freshest value).
	res := tx.Model(&storage.Coupon{}).
		Where("id = ? AND (total = 0 OR used < total)", cp.ID).
		Update("used", gorm.Expr("used + 1"))
	if res.Error != nil {
		return 0, cp, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, cp, errors.New("优惠码已领完")
	}
	return couponDiscount(cp.Kind, cp.Value, price), cp, nil
}

// couponSnapshot is the discount snapshot (orders persist promo_snapshot): list price/discount/code/amount paid,
// so details remain auditable after the code row is deleted.
func couponSnapshot(code string, kind string, value, list, paid int64) string {
	b, _ := json.Marshal(map[string]any{
		"code": code, "kind": kind, "value": value,
		"list_amount_cents": list, "discount_cents": list - paid, "amount_cents": paid,
	})
	return string(b)
}
