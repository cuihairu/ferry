package retention

import (
	"context"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Historical-data retention (F-1910): probe_reports and node_traffic_logs
// grow without bound on a busy fleet; both feed only sliding windows
// (pathhealth reads ~30m of probes, cost sums the current month), so each
// gets a day-based retention swept hourly. User traffic_logs are DELIBERATELY
// out of scope: the accounting ledger is never deleted or modified
// (storage/models.go TrafficLog policy), and audit_log keeps its own sweep
// (internal/audit, AU-3).

// sweepInterval is the sweep tick period: retention granularity is days, so
// an hourly tick is enough and each pass is one indexed DELETE.
var sweepInterval = time.Hour

// PruneProbeReports deletes probe reports older than the retention days and
// returns the number deleted; days<=0 keeps everything.
func PruneProbeReports(db *gorm.DB, days int, now time.Time) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	res := db.Where("probed_at < ?", now.AddDate(0, 0, -days)).Delete(&storage.ProbeReport{})
	return res.RowsAffected, res.Error
}

// PruneNodeTrafficLogs deletes node traffic samples older than the retention
// days and returns the number deleted; days<=0 keeps everything.
func PruneNodeTrafficLogs(db *gorm.DB, days int, now time.Time) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	res := db.Where("recorded_at < ?", now.AddDate(0, 0, -days)).Delete(&storage.NodeTrafficLog{})
	return res.RowsAffected, res.Error
}

// Sweep runs both prunes once and reports the deleted counts.
func Sweep(db *gorm.DB, probeDays, nodeTrafficDays int, now time.Time) (int64, int64, error) {
	pn, err := PruneProbeReports(db, probeDays, now)
	tn, err2 := PruneNodeTrafficLogs(db, nodeTrafficDays, now)
	if err2 != nil {
		err = err2
	}
	return pn, tn, err
}

// Loop sweeps out-of-window history rows once an hour; a days value <= 0
// means permanent retention for that table (its prune is skipped).
func Loop(ctx context.Context, db *gorm.DB, probeDays, nodeTrafficDays int, logger *log.Logger) {
	if probeDays <= 0 && nodeTrafficDays <= 0 {
		return
	}
	if logger == nil {
		logger = log.Default()
	}
	logger.Printf("retention loop started: probe_days=%d node_traffic_days=%d", probeDays, nodeTrafficDays)
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pn, tn, err := Sweep(db, probeDays, nodeTrafficDays, time.Now())
			if err != nil {
				logger.Printf("retention sweep: %v", err)
				continue
			}
			if pn > 0 || tn > 0 {
				logger.Printf("retention sweep: removed %d probe rows, %d node-traffic rows", pn, tn)
			}
		}
	}
}
