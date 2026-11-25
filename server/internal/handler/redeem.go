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

// errRedeemFailed is the single message for any failed redemption: it does not
// distinguish missing/used/disabled/expired codes, preventing enumeration
// (Payment Design §3.3).
var errRedeemFailed = errors.New("兑换失败")

// errCardCodeUnusable means the redemption failed because the card code itself
// is unusable (missing/used/disabled, i.e. the atomic write-off in redeemTx
// step 1 touched 0 rows). Only such failures accrue fail_count (PAY-5 probing
// protection); other failure reasons like a missing user or an expired batch
// are not blamed on the card — otherwise submitting one usable card code
// repeatedly against an invalid user_id would reach 5 hits and auto-disable it
// (P2 fix for malicious card disabling via failed redemption: only the card's
// own unavailability counts, so non-card faults do not harm usable cards).
var errCardCodeUnusable = errors.New("card code unusable")

// redeemInput is the redemption request payload. user_id is passed explicitly
// by the caller in P0 and will come from the session once the user portal is
// ready (it will then be tightened to be non-optional).
type redeemInput struct {
	Code   string `json:"code"`
	UserID uint   `json:"user_id"`
}

// redeem redeems a card code (PAY-4): atomic write-off -> transactional three-
// ledger write (provider=card) -> grant fulfilment.
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
			// Only the card's own unavailability accrues failures (P2: non-card
			// faults such as a missing user are not blamed on the card, so an
			// invalid user_id cannot maliciously disable a usable card).
			if errors.Is(err, errCardCodeUnusable) {
				h.bumpCardFailCount(code)
			}
			fail(c, http.StatusBadRequest, errRedeemFailed)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	h.redeemLimiter.Reset(ip)
	c.JSON(http.StatusOK, result)
}

// redeemTx performs a single redemption. Any failed step rolls the whole thing
// back and leaves the code unused.
func (h *Handler) redeemTx(code string, userID uint, now time.Time) (gin.H, error) {
	var out gin.H
	err := h.db.Transaction(func(tx *gorm.DB) error {
		// 1. Atomic write-off: 0 rows from the conditional update means the code
		// cannot be redeemed (missing/used/disabled).
		res := tx.Model(&storage.CardCode{}).
			Where("code = ? AND status = ?", code, "unused").
			Updates(map[string]any{"status": "used", "used_by": userID, "used_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// The card itself is unusable (missing/used/disabled): blame it, so
			// the caller accrues fail_count (P2 anti-malicious-disable; other
			// failure reasons do not count).
			return fmt.Errorf("%w: %w", errRedeemFailed, errCardCodeUnusable)
		}
		var codeRow storage.CardCode
		if err := tx.Where("code = ?", code).First(&codeRow).Error; err != nil {
			return err
		}
		var batch storage.CardBatch
		if err := tx.First(&batch, codeRow.BatchID).Error; err != nil {
			return err
		}
		// Expired batch: the write-off already rolled back with the transaction.
		if batch.ExpiredAt != nil && batch.ExpiredAt.Before(now) {
			return errRedeemFailed
		}
		// 2. Validate the user.
		var u storage.User
		if err := tx.First(&u, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errRedeemFailed
			}
			return err
		}
		// 3. Three ledgers: order (card/0/paid) -> transaction
		// (external_id = card code id, the unique constraint backstops idempotency)
		// -> grant.
		orderNo := fmt.Sprintf("card-%d-%d", codeRow.ID, now.UnixNano())
		order := storage.PaymentOrder{
			OrderNo: orderNo, UserID: userID, Provider: "card", AmountCents: 0,
			Product: "卡密 " + batch.Name, Status: "paid", PaidAt: &now, CreatedAt: now,
			DistributorID: batch.DistributorID, // DS-1: attribution comes with the card batch, tying the three ledgers together
		}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}
		// DS-1 distributor booking: a distributor batch with a listed price gets
		// two rows, sale (trace only, not balance) + commission (price x
		// commission ratio, into balance), sharing order_no with the three
		// ledgers; self-run batches book nothing. A deactivated distributor does
		// not stop booking — a sold card must always be honoured and the payout
		// keeps its ledger rows.
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
		// 4. Fulfil the grant and write the grant record.
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

// grantSnapshot is the entitlement snapshot before and after a grant.
type grantSnapshot struct {
	QuotaBytes int64      `json:"quota_bytes"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

// applyGrant fulfils the entitlement and writes the grant record inside the
// transaction (shared by card redemption and the online payment callback):
// add_quota raises the quota; everything else extends expiry on the extend_days
// convention (starting from now when there is no expiry). extra is merged into
// the snapshot (card code/batch, the online payment trade_id, etc.); a failure
// rolls back the whole transaction. The write-back is optimistically guarded on
// the values read and only writes the columns this grant touches; when the guard
// misses (a concurrent refund reclamation or other writer touching the row) it
// rereads and recomputes up to three times, refreshing u with the reread — a
// one-sided guard is not enough, and a plain write would silently overwrite the
// other party's committed change back to the stale value (codex deep review P2
// lost-update race).
func applyGrant(tx *gorm.DB, orderNo string, u *storage.User, grantType string, grantValue int64, now time.Time, extra map[string]any) error {
	before := grantSnapshot{QuotaBytes: u.QuotaBytes, ExpiresAt: u.ExpiresAt}
	after := before
	for attempt := 0; attempt < 3; attempt++ {
		if grantType == "add_quota" {
			res := tx.Model(&storage.User{}).
				Where("id = ? AND quota_bytes = ?", u.ID, u.QuotaBytes).
				Update("quota_bytes", u.QuotaBytes+grantValue)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 1 {
				u.QuotaBytes += grantValue
				after = grantSnapshot{QuotaBytes: u.QuotaBytes, ExpiresAt: u.ExpiresAt}
				break
			}
		} else {
			base := now
			if u.ExpiresAt != nil && u.ExpiresAt.After(now) {
				base = *u.ExpiresAt
			}
			ext := base.Add(time.Duration(grantValue) * 24 * time.Hour)
			q := tx.Model(&storage.User{}).Where("id = ?", u.ID)
			if u.ExpiresAt == nil {
				q = q.Where("expires_at IS NULL")
			} else {
				q = q.Where("expires_at = ?", *u.ExpiresAt)
			}
			res := q.Update("expires_at", ext)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 1 {
				u.ExpiresAt = &ext
				after = grantSnapshot{QuotaBytes: u.QuotaBytes, ExpiresAt: u.ExpiresAt}
				break
			}
		}
		if attempt == 2 {
			return errors.New("apply grant: concurrent user update contention")
		}
		// Guard missed: another writer changed the row concurrently (e.g. a
		// refund reclamation); reread, recompute against the new values, retry.
		if err := tx.First(u, u.ID).Error; err != nil {
			return err
		}
	}
	snap := map[string]any{
		"before": before,
		"after":  after,
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
	// Event trigger (NT-2): an in-app system message for the credited grant,
	// atomic with the grant in the same transaction; out-of-app delivery goes
	// through the same event interface (HERALD-4), also atomic (orderNo is the
	// dedup key).
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

// grantNotifTitle is the credited-grant notification copy: traffic as a human
// readable magnitude, duration as days.
func grantNotifTitle(grantType string, value int64) string {
	if grantType == "add_quota" {
		return fmt.Sprintf("流量已到账：+%s", humanBytes(value))
	}
	return fmt.Sprintf("时长已到账：+%d 天", value)
}

// humanBytes humanizes a byte magnitude (GB rounded up to match the purchase
// convention, below 1GB in MB).
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

// normalizeCardCode normalizes the input: uppercased and stripped of stray
// characters, then regrouped as XXXX-XXXX-XXXX to match the stored format (so
// pastes with and without dashes both match).
func normalizeCardCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	raw := b.String()
	if len(raw) != 12 {
		return raw // a wrong length simply fails the write-off on its own
	}
	return raw[:4] + "-" + raw[4:8] + "-" + raw[8:]
}

// bumpCardFailCount accrues failure counts on an existing code row when a
// redemption fails (an unknown code has no row to count); an unused code that
// reaches 5 hits is auto-disabled. Done atomically to avoid read-modify-write
// (PAY-5).
func (h *Handler) bumpCardFailCount(code string) {
	_ = h.db.Model(&storage.CardCode{}).
		Where("code = ?", code).
		Updates(map[string]any{
			"fail_count": gorm.Expr("fail_count + 1"),
			"status": gorm.Expr(
				"CASE WHEN status = 'unused' AND fail_count + 1 >= 5 THEN 'disabled' ELSE status END"),
		}).Error // a counting failure must not affect the unified failure message
}
