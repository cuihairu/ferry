// Package monitor 周期聚合探测结论：判定区域/运营商故障，
// 落状态灯数据并合并告警——一个维度一条告警，不逐节点告警。
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/aggregate"
	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 状态灯取值。
const (
	StateHealthy  = "healthy"
	StateDegraded = "degraded"
	StateFailed   = "failed"
)

// Run 周期执行聚合判定直到 ctx 取消；仅状态迁移时告警。
func Run(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTimer(0) // 启动即跑一轮
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := sweep(db, logger); err != nil {
			logger.Printf("dimension sweep: %v", err)
		}
		t.Reset(interval)
	}
}

// sweep 执行一轮聚合判定并逐维度落库。
func sweep(db *gorm.DB, logger *log.Logger) error {
	verdicts, err := aggregate.Judge(db, aggregate.Options{})
	if err != nil {
		return err
	}
	for _, v := range verdicts {
		if err := applyVerdict(db, logger, v); err != nil {
			return err
		}
	}
	return nil
}

// applyVerdict 落一条维度状态；状态迁移时发合并告警或恢复日志。
func applyVerdict(db *gorm.DB, logger *log.Logger, v aggregate.Verdict) error {
	state := StateHealthy
	switch {
	case v.Failed:
		state = StateFailed
	case v.Sick > 0:
		state = StateDegraded
	}
	reason := fmt.Sprintf("%d/%d 节点异常", v.Sick, v.Total)
	if len(v.SickNodes) > 0 {
		reason += "（" + joinNodes(v.SickNodes) + "）"
	}

	old := "" // 新记录无旧状态
	var row storage.DimensionStatus
	err := db.Where("scope = ? AND key = ?", string(v.Dimension), v.Scope).First(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		row = storage.DimensionStatus{
			Scope: string(v.Dimension), Key: v.Scope,
			State: state, Reason: reason,
			Since: time.Now(), UpdatedAt: time.Now(),
		}
		if err := db.Create(&row).Error; err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if row.State == state {
			row.UpdatedAt = time.Now()
			row.Reason = reason
			return db.Save(&row).Error // 状态未迁移，不重复告警
		}
		old = row.State
		row.State = state
		row.Reason = reason
		row.Since = time.Now()
		row.UpdatedAt = time.Now()
		if err := db.Save(&row).Error; err != nil {
			return err
		}
	}

	// 状态迁移：一个维度一条合并告警，不逐节点告警。
	switch {
	case state == StateFailed:
		logger.Printf("alarm: %s %s 整体不可达: %s，同维度一起切走",
			v.Dimension, v.Scope, reason)
	case state == StateDegraded:
		logger.Printf("degraded: %s %s: %s", v.Dimension, v.Scope, reason)
	case old != "":
		logger.Printf("recovered: %s %s 恢复正常", v.Dimension, v.Scope)
	}

	// 事件外发（P1-10）：与日志同条件推送；失败只记日志不阻断聚合。
	if notifier := notify.FromDB(db); notifier.Enabled() {
		var ev notify.Event
		switch {
		case state == StateFailed:
			ev = notify.Event{Event: "dimension.failed",
				Text: fmt.Sprintf("%s %s 整体不可达: %s，同维度一起切走", v.Dimension, v.Scope, reason),
				Fields: map[string]any{"scope": string(v.Dimension) + "/" + v.Scope, "sick": v.Sick, "total": v.Total}}
		case state == StateDegraded:
			ev = notify.Event{Event: "dimension.degraded",
				Text:   fmt.Sprintf("%s %s: %s", v.Dimension, v.Scope, reason),
				Fields: map[string]any{"scope": string(v.Dimension) + "/" + v.Scope, "sick": v.Sick, "total": v.Total}}
		case old != "":
			ev = notify.Event{Event: "dimension.recovered",
				Text:   fmt.Sprintf("%s %s 恢复正常", v.Dimension, v.Scope),
				Fields: map[string]any{"scope": string(v.Dimension) + "/" + v.Scope}}
		}
		if ev.Event != "" {
			if err := notifier.Send(ev); err != nil {
				logger.Printf("notify webhook: %v", err)
			}
		}
	}
	return nil
}

func joinNodes(nodes []uint64) string {
	parts := make([]string, 0, len(nodes))
	for _, n := range nodes {
		parts = append(parts, strconv.FormatUint(n, 10))
	}
	return strings.Join(parts, ",")
}
