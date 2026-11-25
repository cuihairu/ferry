// Package save aggregates the traffic-savings report (SAVE-7): it periodically scans new rows in
// node_traffic_logs (by an auto-increment id watermark) and incrementally accumulates them into
// save_stats keyed by (node_id, UTC day); the watermark is stored in settings -- with no watermark (first run/
// loss) save_stats is cleared and rebuilt from scratch, guaranteeing no double counting. Converted costs are not stored: the API side computes them on the fly from the node's traffic unit price (a unit-price change does not rewrite history).
package save

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// settingWatermark is the greatest node_traffic_logs id aggregated so far (the settings key).
const settingWatermark = "save_stats_watermark"

// Sweep aggregates one round: it accumulates the traffic rows after the watermark into save_stats day by day
// and advances the watermark. It returns the number of traffic rows aggregated this round.
func Sweep(db *gorm.DB) (int, error) {
	var wm int64
	v, ok, err := storage.GetSetting(db, settingWatermark)
	if err != nil {
		return 0, err
	}
	if ok {
		wm, _ = strconv.ParseInt(v, 10, 64)
	} else if err := db.Where("1=1").Delete(&storage.SaveStat{}).Error; err != nil {
		return 0, err // no watermark: clear and rebuild, so historical rows are not accumulated twice
	}

	var rows []storage.NodeTrafficLog
	if err := db.Where("id > ?", wm).Order("id ASC").Find(&rows).Error; err != nil {
		return 0, err
	}
	type key struct {
		node uint
		day  string
	}
	type aggRow struct {
		direct, blocked int64
	}
	agg := map[key]*aggRow{}
	maxID := wm
	for _, r := range rows {
		if id := int64(r.ID); id > maxID {
			maxID = id
		}
		k := key{r.NodeID, r.RecordedAt.UTC().Format("2006-01-02")}
		a := agg[k]
		if a == nil {
			a = &aggRow{}
			agg[k] = a
		}
		a.direct += r.DirectBytes
		a.blocked += r.BlockedBytes
	}
	for k, d := range agg {
		if err := add(db, k.node, k.day, d.direct, d.blocked); err != nil {
			return 0, err
		}
	}
	if err := storage.SetSetting(db, settingWatermark, strconv.FormatInt(maxID, 10)); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// add accumulates the daily delta (the row is created if absent; a SQL expression accumulation avoids a read-modify-write race).
func add(db *gorm.DB, nodeID uint, day string, direct, blocked int64) error {
	var row storage.SaveStat
	err := db.Where("node_id=? AND day=?", nodeID, day).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(&storage.SaveStat{
			NodeID: nodeID, Day: day, DirectBytes: direct, BlockedBytes: blocked,
		}).Error
	}
	if err != nil {
		return err
	}
	return db.Model(&row).Updates(map[string]any{
		"direct_bytes":  gorm.Expr("direct_bytes + ?", direct),
		"blocked_bytes": gorm.Expr("blocked_bytes + ?", blocked),
	}).Error
}

// Loop aggregates periodically until ctx is cancelled (matching quota.LinkLoop: one round runs at startup).
func Loop(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if n, err := Sweep(db); err != nil {
			logger.Printf("save stats sweep: %v", err)
		} else if n > 0 {
			logger.Printf("save stats: %d traffic row(s) aggregated", n)
		}
		t.Reset(interval)
	}
}
