// Package audit is retention and rotation of the operation audit log (AU-3, promoted to production by P2-6):
// audit_logs are swept rolling by retention days (an hourly tick, a cheap DELETE following the FERRY_*_SEC wiring convention),
// and retention<=0 means permanent retention with the loop not started.
package audit

import (
	"context"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// sweepInterval is the sweep tick period: retention granularity is days, so an hourly tick is enough and a
// single indexed DELETE is cheap. Declared as a variable so tests can shorten the period.
var sweepInterval = time.Hour

// Prune deletes audit rows older than the retention days and returns the number deleted.
func Prune(db *gorm.DB, retentionDays int, now time.Time) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := now.AddDate(0, 0, -retentionDays)
	res := db.Where("created_at < ?", cutoff).Delete(&storage.AuditLog{})
	return res.RowsAffected, res.Error
}

// Loop sweeps out-of-window audit rows once an hour; retention<=0 returns directly (permanent retention).
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
