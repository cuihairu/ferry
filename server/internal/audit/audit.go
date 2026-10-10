// Package audit 是操作审计的保留与轮转（AU-3，P2-6 转正）：audit_logs
// 按保留天数滚动清扫（每小时 tick，沿 FERRY_*_SEC 装配惯例的廉价 DELETE），
// retention<=0 表示永久保留、不启动循环。
package audit

import (
	"context"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// sweepInterval 是清扫 tick 周期：保留粒度是天，小时级 tick 足够且单条
// 带索引 DELETE 便宜。
const sweepInterval = time.Hour

// Prune 删除超过保留天数的审计行，返回删除行数。
func Prune(db *gorm.DB, retentionDays int, now time.Time) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := now.AddDate(0, 0, -retentionDays)
	res := db.Where("created_at < ?", cutoff).Delete(&storage.AuditLog{})
	return res.RowsAffected, res.Error
}

// Loop 每小时清扫一次超窗审计行；retention<=0 直接返回（永久保留）。
func Loop(ctx context.Context, db *gorm.DB, retentionDays int, logger *log.Logger) {
	if retentionDays <= 0 {
		return
	}
	if logger == nil {
		logger = log.Default()
	}
	logger.Printf("audit loop started: retention_days=%d", retentionDays)
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := Prune(db, retentionDays, time.Now())
			if err != nil {
				logger.Printf("audit prune: %v", err)
				continue
			}
			if n > 0 {
				logger.Printf("audit prune: removed %d rows older than %dd", n, retentionDays)
			}
		}
	}
}
