// Package review periodically scans tables to disable expired/over-quota users (P0-11, aligned with Marzban review_users).
package review

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Run executes DisableInactive periodically; interval<=0 means disabled.
func Run(ctx context.Context, db *gorm.DB, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := DisableInactive(db, time.Now()); err != nil {
				log.Printf("review users: %v", err)
			} else if n > 0 {
				log.Printf("review users: disabled %d", n)
				// Event outbound (P1-10): one summary per batch disable; failures only log.
				if notifier := notify.FromDB(db); notifier.Enabled() {
					ev := notify.Event{Event: "review.disabled",
						Text:   fmt.Sprintf("已停用 %d 个到期/超限用户", n),
						Fields: map[string]any{"count": n}}
					if err := notifier.Send(ev); err != nil {
						log.Printf("notify webhook: %v", err)
					}
				}
			}
		}
	}
}

// DisableInactive disables enabled users that are expired or over quota in one pass, returning the count.
// The verdict criteria match subscription availability (sub.UserActive): quota=0 means unlimited; used>=quota disables.
// Over-quota is judged in batch per reset_cycle (P1-4): the window start depends only on the cycle and now,
// so the same cycle shares one SQL; none = cumulative total.
func DisableInactive(db *gorm.DB, now time.Time) (int64, error) {
	var total int64
	res := db.Model(&storage.User{}).
		Where("enabled = ? AND expires_at IS NOT NULL AND expires_at < ?", true, now).
		Update("enabled", false)
	if res.Error != nil {
		return total, res.Error
	}
	total += res.RowsAffected
	for _, cycle := range []string{quota.CycleNone, quota.CycleDay, quota.CycleWeek, quota.CycleMonth} {
		n, err := disableOverQuota(db, cycle, now)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// disableOverQuota disables enabled users over quota within one cycle.
func disableOverQuota(db *gorm.DB, cycle string, now time.Time) (int64, error) {
	sub := "SELECT COALESCE(SUM(rx_bytes + tx_bytes), 0) FROM traffic_logs WHERE traffic_logs.user_id = users.id"
	args := []any{true, cycle}
	if since := quota.WindowStart(cycle, now); !since.IsZero() {
		sub += " AND recorded_at >= ?"
		args = append(args, since)
	}
	res := db.Model(&storage.User{}).
		Where("enabled = ? AND reset_cycle = ? AND quota_bytes > 0 AND quota_bytes <= ("+sub+")", args...).
		Update("enabled", false)
	return res.RowsAffected, res.Error
}
