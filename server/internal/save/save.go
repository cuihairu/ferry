// Package save 流量节省报表聚合（SAVE-7）：周期扫 node_traffic_logs 新增行
// （自增 id 水位），按 (node_id, UTC 日) 增量累计进 save_stats；水位存
// settings——无水位（首次/丢失）先清空 save_stats 全量重建，保证不重复
// 累计。折算费用不入库：API 侧按节点流量单价现算（单价调整不回改历史）。
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

// settingWatermark 已聚合到的 node_traffic_logs 最大 id（settings 键）。
const settingWatermark = "save_stats_watermark"

// Sweep 聚合一次：把水位之后的流量行按日累计进 save_stats 并推进水位。
// 返回本轮聚合的流量行数。
func Sweep(db *gorm.DB) (int, error) {
	var wm int64
	v, ok, err := storage.GetSetting(db, settingWatermark)
	if err != nil {
		return 0, err
	}
	if ok {
		wm, _ = strconv.ParseInt(v, 10, 64)
	} else if err := db.Where("1=1").Delete(&storage.SaveStat{}).Error; err != nil {
		return 0, err // 无水位：清空重建，防历史行重复累计
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

// add 按日累计增量（行不存在先建；SQL 表达式累加防读改写竞态）。
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

// Loop 周期聚合直到 ctx 取消（对齐 quota.LinkLoop：启动即跑一轮）。
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
