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

// 优惠码（PROMO-1，运营设计 §5）：管理面 CRUD + panel 下单原子核销。
// 取优判定（DS §3.3）：一单一优惠源不叠加——下单至多应用一张码，兑换链路
// 无支付（面价在批次）无叠加点；佣金口径不变（在线单不落佣金，兑换按面价）。

// couponInput 是建号/编辑载荷（部分更新走指针字段）。
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

// listCoupons 优惠码列表（GET /api/coupons）：id DESC，dash 全量管理。
func (h *Handler) listCoupons(c *gin.Context) {
	var rows []storage.Coupon
	if err := h.db.Order("id DESC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// validateCouponInput 校验载荷并回错误：kind 枚举、pct 基点 1-9999、
// cut>0、门槛>=0、total/per_user>=0、scope 形态与批次存在性。
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

// validateDiscountRules 校验折扣规则四元组（优惠码与活动 rules 共用口径）：
// kind 枚举、pct 基点 1-9999、cut>0、门槛>=0、scope 形态与批次存在性。
// 返回归一化后的 scope（空→all）。
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

// parseBatchScope 解析 batch:<id> 形态。
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

// createCoupon 新建优惠码（POST /api/coupons）：code 缺省自动生成（16 位
// 大写数字，FERRY-XXXX 形态与卡密可读性同口径），重复码 409。
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
		in.PerUser = 1 // 设计缺省：每用户 1 次
	}
	row := storage.Coupon{
		Code: code, Kind: in.Kind, Value: in.Value, Scope: in.Scope,
		MinAmount: in.MinAmount, StartsAt: in.StartsAt, EndsAt: in.EndsAt,
		Total: in.Total, PerUser: in.PerUser, CreatedAt: time.Now(),
	}
	if err := h.db.Create(&row).Error; err != nil {
		// TranslateError 开启时 ErrDuplicatedKey 可判；字符串兜底盖方言差异。
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

// updateCoupon 编辑优惠码（PUT /api/coupons/:id）：整包更新（除 used 只读）；
// ends_at 置当下即停用。密码类敏感面无——优惠码本身可回显。
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
		// 改码查重：指向他行即冲突。
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

// deleteCoupon 删除优惠码（DELETE /api/coupons/:id）：已核销订单的快照
// 留档不受影响（promo_snapshot 自含明细）。
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

// randomCode8 取 8 位大写数字随机段（优惠码缺省生成用）。
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

// couponDiscount 计算折扣后实付（PROMO-1）：cut 减额下限 0；pct 基点向下取整。
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

// couponCheck 加载并校验优惠码（只读，不核销）：存在性/窗口/限次/适用
// 范围/门槛/每用户限次逐项过；失败返回业务文案错误（panel 可读）。
// 下单两段式里预检与本事务核销共用本函数，保证口径一致。
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
	var mine int64
	if err := tx.Model(&storage.PaymentOrder{}).
		Where("user_id = ? AND promo_code = ? AND status != ? AND status != ?", u.ID, cp.Code, "failed", "expired").
		Count(&mine).Error; err != nil {
		return cp, err
	}
	if cp.PerUser > 0 && mine >= cp.PerUser {
		return cp, errors.New("优惠码每用户限用一次")
	}
	return cp, nil
}

// applyCouponTx 在下单事务内原子核销优惠码（PROMO-1）：couponCheck 只读
// 校验通过后，条件 UPDATE used+1 防并发超发（total=0 不限）。返回实付与
// 码行；核销失败由调用方整事务回滚（订单行同事务，码与订单同生同灭）。
func applyCouponTx(tx *gorm.DB, code string, u *storage.User, batch *storage.CardBatch, price int64, now time.Time) (int64, storage.Coupon, error) {
	cp, err := couponCheck(tx, code, u, batch, price, now)
	if err != nil {
		return 0, cp, err
	}
	// 条件更新核销：total 限次并发兜底（used 由本行最新值判定）。
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

// couponSnapshot 优惠快照（订单落 promo_snapshot）：原价/折扣/码/实付，
// 码行删除后明细仍可对账。
func couponSnapshot(code string, kind string, value, list, paid int64) string {
	b, _ := json.Marshal(map[string]any{
		"code": code, "kind": kind, "value": value,
		"list_amount_cents": list, "discount_cents": list - paid, "amount_cents": paid,
	})
	return string(b)
}
