// Package notifyscan triggers in-app notifications automatically (NT-2): a timed
// sweep over two kinds, expiry and traffic threshold, each tiered once and deduped
// per day (at most one of the same type per user per day); the
// grant-received event triggers land inside the handler.applyGrant transaction and the announcement fan-out sits in the admin API, neither going through this package;
package notifyscan

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/cuihairu/ferry/server/internal/announce"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// expiryDaysWindow is the expiry reminder window: remind within 7 days (including already expired).
const expiryDaysWindow = 7

// Traffic anomaly detection (F-1252): a user's usage so far today is compared
// against the average daily usage of the trailing seven full calendar days
// (empty days count as zero); today >= spike ratio * baseline with a baseline
// above the floor fires one admin alert, deduped per user per day. The floor
// keeps near-zero baselines (new or barely-used accounts) from amplifying
// rounding noise into alerts.
const (
	anomalyBaselineDays  = 7
	anomalySpikeRatio    = 5
	anomalyBaselineFloor = int64(1) << 20 // 1 MiB
)

// Loop is the timed sweep (matching the Loop convention of recovery/cert): interval comes
// from config; 1h order of magnitude is suggested - dedup is per day, so a tighter sweep can never over-send.
func Loop(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := Sweep(db, time.Now()); err != nil {
				logger.Printf("notifyscan: %v", err)
			}
		}
	}
}

// Sweep runs one sweep round; expiry, traffic and scheduled announcements are each independent, never blocking each other, with errors joined and returned.
func Sweep(db *gorm.DB, now time.Time) error {
	return errors.Join(sweepExpiry(db, now), sweepTraffic(db, now), sweepAnnouncements(db, now), sweepTrafficAnomaly(db, now))
}

// sweepAnnouncements fans out due scheduled announcements (P2-5, announce.RunDue).
func sweepAnnouncements(db *gorm.DB, now time.Time) error {
	_, err := announce.RunDue(db, now)
	return err
}

// sweepExpiry is the expiry sweep: enabled + preference not turned off + expiring within 7 days
// (including already expired) users each get one reminder; those already sent today are skipped.
func sweepExpiry(db *gorm.DB, now time.Time) error {
	var users []storage.User
	horizon := now.AddDate(0, 0, expiryDaysWindow)
	if err := db.Where("enabled = ? AND notify_expiry = ? AND expires_at IS NOT NULL AND expires_at <= ?",
		true, true, horizon).Find(&users).Error; err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}
	sent, err := notifiedToday(db, storage.NotifExpiry, now)
	if err != nil {
		return err
	}
	for i := range users {
		u := users[i]
		if sent[int64(u.ID)] || u.ExpiresAt == nil {
			continue
		}
		if err := db.Create(&storage.Notification{
			UserID: int64(u.ID), Type: storage.NotifExpiry,
			Title: expiryText(*u.ExpiresAt, now), CreatedAt: now,
		}).Error; err != nil {
			return err
		}
		// Out-of-site delivery goes through the same event interface (HERALD-4): it is sent only
		// after the in-app notification is written; dedup rides on the notification gate. Emit returns an error only on write failure (same DB as the notification) and does not block the sweep.
		_, _ = herald.Emit(db, herald.EmitInput{
			Kind: herald.KindExpire, Severity: herald.SeverityInfo,
			Title:    expiryText(*u.ExpiresAt, now),
			Target:   herald.TargetUser(int64(u.ID)),
			DedupKey: fmt.Sprintf("expire:%d:%s", u.ID, now.Format("20060102")),
			Meta:     map[string]any{"user_id": u.ID, "expires_at": u.ExpiresAt.Format(time.RFC3339)},
		})
	}
	return nil
}

// expiryText is the expiry wording: the difference is rounded up to whole days (under a day still counts as 1 day).
func expiryText(exp, now time.Time) string {
	days := int(math.Ceil(exp.Sub(now).Hours() / 24))
	switch {
	case days > 0:
		return fmt.Sprintf("您的账号将于 %d 天后到期，请及时续期", days)
	case days == 0:
		return "您的账号今天到期，请及时续期"
	default:
		return fmt.Sprintf("您的账号已到期 %d 天", -days)
	}
}

// sweepTraffic is the traffic sweep: enabled + preference not turned off + users with a quota, computing usage over each user's own reset-cycle window and reminding only when the
// personal threshold is reached (unset/out-of-range falls back to 80); those already sent today are skipped.
func sweepTraffic(db *gorm.DB, now time.Time) error {
	var users []storage.User
	if err := db.Where("enabled = ? AND notify_traffic = ? AND quota_bytes > 0",
		true, true).Find(&users).Error; err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}
	sent, err := notifiedToday(db, storage.NotifTraffic, now)
	if err != nil {
		return err
	}
	for i := range users {
		u := users[i]
		if sent[int64(u.ID)] {
			continue
		}
		used, err := quota.UsedBytes(db, u.ID, u.ResetCycle, now)
		if err != nil {
			return err
		}
		pct := int(used * 100 / u.QuotaBytes)
		warn := u.TrafficWarnPercent
		if warn <= 0 || warn > 100 {
			warn = 80
		}
		if pct < warn {
			continue
		}
		title := fmt.Sprintf("流量预警：本周期已用 %d%%", pct)
		if err := db.Create(&storage.Notification{
			UserID: int64(u.ID), Type: storage.NotifTraffic,
			Title: title, CreatedAt: now,
		}).Error; err != nil {
			return err
		}
		// Out-of-site delivery goes through the same event interface (HERALD-4), with the same rules as the expiry sweep.
		_, _ = herald.Emit(db, herald.EmitInput{
			Kind: herald.KindQuota, Severity: herald.SeverityInfo,
			Title:    title,
			Target:   herald.TargetUser(int64(u.ID)),
			DedupKey: fmt.Sprintf("quota:%d:%s", u.ID, now.Format("20060102")),
			Meta:     map[string]any{"user_id": u.ID, "used_percent": pct, "warn_percent": warn},
		})
	}
	return nil
}

// sweepTrafficAnomaly is the traffic anomaly sweep (F-1252): every enabled user's
// usage so far today is compared against the trailing-baseline average (see the
// package constants); a spike fires one admin-facing Herald alert, deduped per
// user per day. It is admin-facing on purpose -- it says nothing about quota, so
// the user's notify preference does not gate it. Detection failure for one user
// does not abort the sweep over the rest.
func sweepTrafficAnomaly(db *gorm.DB, now time.Time) error {
	var users []storage.User
	if err := db.Where("enabled = ?", true).Find(&users).Error; err != nil {
		return err
	}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	windowStart := dayStart.AddDate(0, 0, -anomalyBaselineDays)
	type daySum struct {
		Date string
		Sum  int64
	}
	for i := range users {
		u := users[i]
		var rows []storage.TrafficLog
		if err := db.Select("rx_bytes", "tx_bytes", "recorded_at").
			Where("user_id = ? AND recorded_at >= ?", u.ID, windowStart).
			Order("recorded_at").Find(&rows).Error; err != nil {
			return err
		}
		sums := map[string]int64{}
		for _, r := range rows {
			sums[r.RecordedAt.Format("2006-01-02")] += r.RxBytes + r.TxBytes
		}
		var baseline int64
		for d := 1; d <= anomalyBaselineDays; d++ {
			baseline += sums[dayStart.AddDate(0, 0, -d).Format("2006-01-02")]
		}
		baseline /= anomalyBaselineDays
		if baseline < anomalyBaselineFloor {
			continue
		}
		today := sums[dayStart.Format("2006-01-02")]
		if today < anomalySpikeRatio*baseline {
			continue
		}
		dedup := fmt.Sprintf("traffic_anomaly:%d:%s", u.ID, now.Format("2006-01-02"))
		var n int64
		if err := db.Model(&storage.Event{}).
			Where("kind = ? AND dedup_key = ?", herald.KindTrafficAnomaly, dedup).
			Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		_, _ = herald.Emit(db, herald.EmitInput{
			Kind:     herald.KindTrafficAnomaly,
			Severity: herald.SeverityWarning,
			Title:    "流量异常",
			Body: fmt.Sprintf("用户 %s（id=%d）今日用量 %d MiB 已达近 %d 日均值（%d MiB）的 %d 倍，疑似异常或共享。",
				u.Username, u.ID, today>>20, anomalyBaselineDays, baseline>>20, anomalySpikeRatio),
			Target:   herald.TargetAdmin,
			DedupKey: dedup,
			Meta: map[string]any{
				"user_id": u.ID, "baseline_bytes": baseline, "today_bytes": today,
				"ratio": anomalySpikeRatio, "baseline_days": anomalyBaselineDays,
			},
		})
	}
	return nil
}

// notifiedToday returns the set of users already sent this notification type today (per-day dedup, at most one per day).
func notifiedToday(db *gorm.DB, typ string, now time.Time) (map[int64]bool, error) {
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var ids []int64
	err := db.Model(&storage.Notification{}).
		Where("type = ? AND created_at >= ?", typ, dayStart).
		Distinct().Pluck("user_id", &ids).Error
	if err != nil {
		return nil, err
	}
	sent := make(map[int64]bool, len(ids))
	for _, id := range ids {
		sent[id] = true
	}
	return sent, nil
}
