package monitor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// AlertKindOverload is the alert kind for a bandwidth-utilization overload
// (F-1067), alongside the agent-reported kinds (proc_crash/cert_expiry/
// high_load/config_error).
const AlertKindOverload = "overload"

// OverloadUtilPct is the bandwidth utilization at or above which a node is
// warned as overloaded (F-1067): the same 80% line the panel load view
// paints, now persisted server-side.
const OverloadUtilPct = 80

// OverloadSweep evaluates the unified utilization (F-1064) for every node:
// at or above the threshold one active overload alert per node is upserted
// (a repeated round refreshes the message instead of stacking rows); back
// under the threshold the active alert is resolved with a trail. Nodes
// without samples never warn. Returns the active overload alert count.
func OverloadSweep(db *gorm.DB, now time.Time) (int, error) {
	samples, err := storage.LatestLoad(db, now.Add(-storage.LoadFresh))
	if err != nil {
		return 0, err
	}
	var nodes []storage.Node
	if err := db.Find(&nodes).Error; err != nil {
		return 0, err
	}
	active := 0
	for _, n := range nodes {
		s := samples[n.ID]
		util := storage.UtilizationPct(s.Mbps, n.CapacityMbps(), s.Conns > 0 || s.Mbps > 0)
		overloaded := util >= OverloadUtilPct
		var row storage.Alert
		err := db.Where("node_id = ? AND kind = ? AND state = ?", n.ID, AlertKindOverload, "active").
			First(&row).Error
		switch {
		case overloaded && errors.Is(err, gorm.ErrRecordNotFound):
			if err := db.Create(&storage.Alert{
				NodeID: n.ID, Kind: AlertKindOverload, Severity: "warning",
				Message: fmt.Sprintf("带宽利用率 %d%% 超阈值 %d%%", util, OverloadUtilPct),
			}).Error; err != nil {
				return active, err
			}
			active++
		case overloaded && err == nil:
			// same alert refreshes in place: the message carries the newest number
			if err := db.Model(&row).Update("message",
				fmt.Sprintf("带宽利用率 %d%% 超阈值 %d%%", util, OverloadUtilPct)).Error; err != nil {
				return active, err
			}
			active++
		case !overloaded && err == nil:
			// recovered: resolve with a trail instead of deleting the row
			if err := db.Model(&row).Updates(map[string]any{
				"state": "resolved", "resolved_at": now,
			}).Error; err != nil {
				return active, err
			}
		}
	}
	return active, nil
}

// RunOverload executes the overload warning periodically until ctx is
// cancelled (F-1067), on the same cadence the panel refreshes its load view.
func RunOverload(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if _, err := OverloadSweep(db, time.Now()); err != nil {
			logger.Printf("overload sweep: %v", err)
		}
		t.Reset(interval)
	}
}
