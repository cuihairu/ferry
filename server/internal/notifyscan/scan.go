// Package notifyscan 站内信自动触发（NT-2）：定时扫描到期与流量阈值两类，
// 每档跑一次、按日去重（同一用户同一类型一天至多一条）；发放到账的事件
// 触发在 handler.applyGrant 事务内直落，公告扇出在管理 API，都不经过本包。
package notifyscan

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// expiryDaysWindow 到期提醒窗口：7 天内（含已到期）提醒。
const expiryDaysWindow = 7

// Loop 定时扫描（对齐 recovery/cert 的 Loop 口径）：interval 由配置给，
// 建议 1h 量级——去重按日，跑得再勤也不会多发。
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

// Sweep 跑一轮扫描；到期与流量各自独立，互不阻断，错误合并返回。
func Sweep(db *gorm.DB, now time.Time) error {
	return errors.Join(sweepExpiry(db, now), sweepTraffic(db, now))
}

// sweepExpiry 到期扫描：enabled + 未关掉偏好 + 7 天内到期（含已过期）的用户
// 各提醒一条；今天已发过的不再发。
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
	}
	return nil
}

// expiryText 到期文案：差值按整天向上取整（不到一天也算 1 天）。
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

// sweepTraffic 流量扫描：enabled + 未关掉偏好 + 有配额的用户，按各自重置
// 周期窗口算用量，达到个人阈值（未设/越界回落 80）才提醒；今天已发过的不再发。
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
		if err := db.Create(&storage.Notification{
			UserID: int64(u.ID), Type: storage.NotifTraffic,
			Title: fmt.Sprintf("流量预警：本周期已用 %d%%", pct), CreatedAt: now,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// notifiedToday 返回今天已发过该类型通知的用户集合（按日去重，一天至多一条）。
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
