// Package review 周期扫表停用到期/超限用户（P0-11，对齐 Marzban review_users）。
package review

import (
	"context"
	"log"
	"time"

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
			}
		}
	}
}

// DisableInactive 一次性停用已到期与已超配额的启用用户，返回停用数。
// 判定口径与订阅可用性（sub.UserActive）一致：quota=0 不限；used>=quota 即停。
func DisableInactive(db *gorm.DB, now time.Time) (int64, error) {
	var total int64
	res := db.Model(&storage.User{}).
		Where("enabled = ? AND expires_at IS NOT NULL AND expires_at < ?", true, now).
		Update("enabled", false)
	if res.Error != nil {
		return total, res.Error
	}
	total += res.RowsAffected
	res = db.Model(&storage.User{}).
		Where("enabled = ? AND quota_bytes > 0 AND quota_bytes <= ("+
			"SELECT COALESCE(SUM(rx_bytes + tx_bytes), 0) FROM traffic_logs WHERE traffic_logs.user_id = users.id"+
			")", true).
		Update("enabled", false)
	if res.Error != nil {
		return total, res.Error
	}
	total += res.RowsAffected
	return total, nil
}
