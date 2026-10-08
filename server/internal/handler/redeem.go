package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// errRedeemFailed 是兑换失败的统一文案：不区分码不存在/已用/禁用/过期，防枚举（《支付设计》§3.3）。
var errRedeemFailed = errors.New("兑换失败")

// redeemInput 是兑换请求载荷。user_id 在 P0 由调用方显式给定，
// 用户门户就绪后改为从会话取（届时收紧为不可传）。
type redeemInput struct {
	Code   string `json:"code"`
	UserID uint   `json:"user_id"`
}

// redeem 兑换卡密（PAY-4）：原子核销 → 事务写三账（provider=card）→ 执行权益。
func (h *Handler) redeem(c *gin.Context) {
	var in redeemInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, errRedeemFailed)
		return
	}
	code := normalizeCardCode(in.Code)
	if code == "" || in.UserID == 0 {
		fail(c, http.StatusBadRequest, errRedeemFailed)
		return
	}
	ip := c.ClientIP()
	if !h.redeemLimiter.Allow(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试过于频繁，请稍后再试"})
		return
	}

	now := time.Now()
	result, err := h.redeemTx(code, in.UserID, now)
	if err != nil {
		if errors.Is(err, errRedeemFailed) {
			h.redeemLimiter.RecordFailure(ip)
			h.bumpCardFailCount(code)
			fail(c, http.StatusBadRequest, errRedeemFailed)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	h.redeemLimiter.Reset(ip)
	c.JSON(http.StatusOK, result)
}

// redeemTx 执行一次兑换。任一步失败整体回滚，码回到未用状态。
func (h *Handler) redeemTx(code string, userID uint, now time.Time) (gin.H, error) {
	var out gin.H
	err := h.db.Transaction(func(tx *gorm.DB) error {
		// 1. 原子核销：条件更新行数为 0 即不可兑换（不存在/已用/禁用）。
		res := tx.Model(&storage.CardCode{}).
			Where("code = ? AND status = ?", code, "unused").
			Updates(map[string]any{"status": "used", "used_by": userID, "used_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errRedeemFailed
		}
		var codeRow storage.CardCode
		if err := tx.Where("code = ?", code).First(&codeRow).Error; err != nil {
			return err
		}
		var batch storage.CardBatch
		if err := tx.First(&batch, codeRow.BatchID).Error; err != nil {
			return err
		}
		// 过期批次：核销已随事务回滚。
		if batch.ExpiredAt != nil && batch.ExpiredAt.Before(now) {
			return errRedeemFailed
		}
		// 2. 用户校验。
		var u storage.User
		if err := tx.First(&u, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errRedeemFailed
			}
			return err
		}
		// 3. 三账：订单（card/0/paid）→ 流水（external_id=码 id，唯一约束兜底幂等）→ 发放。
		orderNo := fmt.Sprintf("card-%d-%d", codeRow.ID, now.UnixNano())
		order := storage.PaymentOrder{
			OrderNo: orderNo, UserID: userID, Provider: "card", AmountCents: 0,
			Product: "卡密 " + batch.Name, Status: "paid", PaidAt: &now, CreatedAt: now,
			DistributorID: batch.DistributorID, // DS-1：归属随卡批次带入，三账串联
		}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		// DS-1 代理落账：代理批次且有面价 → sale（留痕不进余额）+ commission
		//（面价×佣金比例，进余额）两行，同 order_no 与三账串联；自营批次不落。
		// 代理停用不影响落账——卡已售出权益必兑现，分润照记账目保留。
		if batch.DistributorID > 0 && batch.PriceCents > 0 {
			var dist storage.Distributor
			if err := tx.First(&dist, batch.DistributorID).Error; err != nil {
				return err
			}
			ledger := []storage.DistributorLedger{{
				DistributorID: dist.ID, OrderNo: orderNo, Kind: "sale",
				AmountCents: batch.PriceCents, Note: "售卡 " + batch.Name, CreatedAt: now,
			}}
			if commission := batch.PriceCents * int64(dist.DiscountPercent) / 100; commission > 0 {
				ledger = append(ledger, storage.DistributorLedger{
					DistributorID: dist.ID, OrderNo: orderNo, Kind: "commission",
					AmountCents: commission, Note: "佣金 " + batch.Name, CreatedAt: now,
				})
			}
			if err := tx.Create(&ledger).Error; err != nil {
				return err
			}
		}
		raw, _ := json.Marshal(map[string]string{"code": code, "channel": "redeem"})
		txn := storage.PaymentTransaction{
			OrderNo: orderNo, Provider: "card",
			ExternalID:  fmt.Sprintf("card-%d", codeRow.ID),
			AmountCents: 0, Direction: "in", Raw: string(raw), OccurredAt: now, CreatedAt: now,
		}
		if err := tx.Create(&txn).Error; err != nil {
			return err
		}
		// 4. 执行权益并写发放记录。
		if err := applyGrant(tx, orderNo, &u, batch.GrantType, batch.GrantValue, now,
			map[string]any{"code": code, "batch_id": batch.ID}); err != nil {
			return err
		}
		out = gin.H{
			"order_no": orderNo, "grant_type": batch.GrantType, "grant_value": batch.GrantValue,
			"quota_bytes": u.QuotaBytes, "expires_at": u.ExpiresAt,
		}
		return nil
	})
	return out, err
}

// grantSnapshot 是发放前后的权益快照。
type grantSnapshot struct {
	QuotaBytes int64      `json:"quota_bytes"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

// applyGrant 在事务内执行权益并写发放记录（卡密兑换与在线支付回调共用）：
// add_quota 加配额；其余按 extend_days 口径延到期（无到期自当下起算）。
// extra 并入快照（卡密的码/批次、在线支付的 trade_id 等），失败回滚整个事务。
func applyGrant(tx *gorm.DB, orderNo string, u *storage.User, grantType string, grantValue int64, now time.Time, extra map[string]any) error {
	before := grantSnapshot{QuotaBytes: u.QuotaBytes, ExpiresAt: u.ExpiresAt}
	if grantType == "add_quota" {
		u.QuotaBytes += grantValue
	} else {
		base := now
		if u.ExpiresAt != nil && u.ExpiresAt.After(now) {
			base = *u.ExpiresAt
		}
		ext := base.Add(time.Duration(grantValue) * 24 * time.Hour)
		u.ExpiresAt = &ext
	}
	if err := tx.Model(&storage.User{}).Where("id = ?", u.ID).
		Updates(map[string]any{"quota_bytes": u.QuotaBytes, "expires_at": u.ExpiresAt}).Error; err != nil {
		return err
	}
	snap := map[string]any{
		"before": before,
		"after":  grantSnapshot{QuotaBytes: u.QuotaBytes, ExpiresAt: u.ExpiresAt},
	}
	for k, v := range extra {
		snap[k] = v
	}
	snapJSON, _ := json.Marshal(snap)
	grant := storage.Grant{
		OrderNo: orderNo, UserID: u.ID,
		GrantType: grantType, GrantValue: grantValue,
		Snapshot: string(snapJSON), CreatedAt: now,
	}
	if err := tx.Create(&grant).Error; err != nil {
		return err
	}
	// 事件触发（NT-2）：发放到账落一条 system 站内信，与发放同事务原子；
	// 站外投递走同一事件接口（HERALD-4），同事务原子（orderNo 做去重键）。
	if err := tx.Create(&storage.Notification{
		UserID: int64(u.ID), Type: storage.NotifSystem,
		Title: grantNotifTitle(grantType, grantValue), CreatedAt: now,
	}).Error; err != nil {
		return err
	}
	_, err := herald.Emit(tx, herald.EmitInput{
		Kind: herald.KindOrder, Severity: herald.SeverityInfo,
		Title:    grantNotifTitle(grantType, grantValue),
		Target:   herald.TargetUser(int64(u.ID)),
		DedupKey: fmt.Sprintf("order:%d:%s", u.ID, orderNo),
		Meta:     map[string]any{"user_id": u.ID, "order_no": orderNo, "grant_type": grantType, "grant_value": grantValue},
	})
	return err
}

// grantNotifTitle 发放到账通知文案：流量给人类可读量级，时长给天数。
func grantNotifTitle(grantType string, value int64) string {
	if grantType == "add_quota" {
		return fmt.Sprintf("流量已到账：+%s", humanBytes(value))
	}
	return fmt.Sprintf("时长已到账：+%d 天", value)
}

// humanBytes 字节量级 humanize（GB 向上取整对齐购买口径，不足 1GB 按 MB）。
func humanBytes(n int64) string {
	const gb = 1 << 30
	const mb = 1 << 20
	switch {
	case n >= gb:
		return fmt.Sprintf("%d GB", (n+gb-1)/gb)
	case n >= mb:
		return fmt.Sprintf("%d MB", (n+mb-1)/mb)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// normalizeCardCode 归一化输入：统一大写、去杂字符后还原 XXXX-XXXX-XXXX 分组，
// 与存储格式对齐（带横线与不带横线的粘贴都能匹配）。
func normalizeCardCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	raw := b.String()
	if len(raw) != 12 {
		return raw // 非法长度自然核销失败
	}
	return raw[:4] + "-" + raw[4:8] + "-" + raw[8:]
}

// bumpCardFailCount 兑换失败时对已存在码行累计失败次数（未知码无行可计）；
// unused 码计满 5 次自动禁用，原子完成避免读改写（PAY-5）。
func (h *Handler) bumpCardFailCount(code string) {
	_ = h.db.Model(&storage.CardCode{}).
		Where("code = ?", code).
		Updates(map[string]any{
			"fail_count": gorm.Expr("fail_count + 1"),
			"status": gorm.Expr(
				"CASE WHEN status = 'unused' AND fail_count + 1 >= 5 THEN 'disabled' ELSE status END"),
		}).Error // 计数失败不影响统一失败文案
}
