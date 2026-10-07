// Package review 周期扫表停用到期/超限用户（P0-11，对齐 Marzban review_users）。
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

// Run 周期执行 DisableInactive，interval<=0 表示关闭。
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
				// 事件外发（P1-10）：批量停用汇总一条，失败只记日志。
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

// DisableInactive 一次性停用已到期与已超配额的启用用户，返回停用数。
// 判定口径与订阅可用性（sub.UserActive）一致：quota=0 不限；used>=quota 即停。
// 超限按 reset_cycle 分组批量判定（P1-4）：窗口起点只依赖周期与 now，
// 同周期共享一条 SQL；none=全量累计。
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

// disableOverQuota 停用单一周期内超配额的启用用户。
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
