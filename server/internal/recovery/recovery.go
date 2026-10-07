// Package recovery 是封禁恢复流水线（BR-1）：判封状态机与 L1→L2→L3
// 分级编排。判封口径：入口被 E-16 摘除后持续 SustainedAfter 仍未复位
// （探测仍不可达）→ 判封入流水线；探测恢复（摘挂复位）随时完成。
// 级别动作经 Registry 注册，未注册级别记 skipped 直接推进：
// L1 域名前置 DNS 切换（BR-2 插件位）、L2 IP 池补位（BR-3 插件位）、
// L3 一键开新机（接 §1 供给流水线）。每级超时未恢复进下一级，
// 全级耗尽记 failed（告警升级人工由 BR-5 承接）。
package recovery

import (
	"context"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 状态与级别取值。
const (
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"

	// ActionUnset/ActionRunning 等动作态取值（Recovery.ActionState）。
	ActionUnset   = ""
	ActionRunning = "running"
	ActionOK      = "ok"
	ActionFailed  = "failed"
	ActionSkipped = "skipped"

	// MaxLevel 是恢复三级上限（L1 域名/DNS → L2 IP 池 → L3 开新机）。
	MaxLevel = 3
)

// 判封与推进的默认口径。
const (
	DefaultSustainedAfter = 10 * time.Minute
	DefaultLevelTimeout   = 10 * time.Minute
)

// Options 是判封/推进口径，零值回退默认。
type Options struct {
	// SustainedAfter 是摘除后持续未恢复满此时长才判封（给探测恢复留窗口，
	// 避免瞬时抖动直接开新机）。
	SustainedAfter time.Duration
	// LevelTimeout 是单级动作的等待窗口：超时未恢复进下一级。
	LevelTimeout time.Duration
}

func (o Options) normalize() Options {
	if o.SustainedAfter <= 0 {
		o.SustainedAfter = DefaultSustainedAfter
	}
	if o.LevelTimeout <= 0 {
		o.LevelTimeout = DefaultLevelTimeout
	}
	return o
}

// Action 是一级恢复动作。Run 可能阻塞分钟级（L3 开新机），由引擎在
// goroutine 中执行；结果经行内 ActionState 回写。
type Action interface {
	Name() string
	Run(ctx context.Context, node storage.Node) error
}

// Registry 按级别注册动作（1-3）；缺省级别 skipped 推进。
type Registry map[int]Action

// Loop 是状态机周期循环（interval 0 回退 60s）。
func Loop(ctx context.Context, db *gorm.DB, interval time.Duration, opts Options, reg Registry, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 60 * time.Second
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := Sweep(db, time.Now(), opts, reg, logger); err != nil {
			logger.Printf("recovery sweep: %v", err)
		}
		t.Reset(interval)
	}
}

// Sweep 执行一轮状态机：完成恢复中的、推进等待中的、开新判封的。
// 单线程串行（引擎级），动作 goroutine 只回写各自行的结果字段。
func Sweep(db *gorm.DB, now time.Time, opts Options, reg Registry, logger *log.Logger) error {
	if logger == nil {
		logger = log.Default()
	}
	opts = opts.normalize()
	if reg == nil {
		reg = Registry{}
	}

	// 1) 探测恢复：摘挂复位（pool_state=active）即完成，无条件先收。
	var running []storage.Recovery
	if err := db.Where("state = ?", StateRunning).Find(&running).Error; err != nil {
		return err
	}
	if len(running) > 0 {
		var active []storage.Node
		if err := db.Where("pool_state = ?", pool.StateActive).Find(&active).Error; err != nil {
			return err
		}
		activeSet := make(map[uint]bool, len(active))
		for _, n := range active {
			activeSet[n.ID] = true
		}
		for _, rec := range running {
			if !activeSet[rec.NodeID] {
				continue
			}
			if err := finishRecovery(db, rec.ID, StateDone, "探测恢复，摘挂复位"); err != nil {
				return err
			}
			logger.Printf("recovery done: node=%d %s", rec.NodeID, rec.NodeName)
		}
		// 重取：上一步已收掉恢复中的，剩余即待推进的。
		if err := db.Where("state = ?", StateRunning).Find(&running).Error; err != nil {
			return err
		}
	}

	// 2) 推进：逐行驱动当前级（启动动作 / 超时进下一级 / 耗尽记失败）。
	for _, rec := range running {
		node, err := nodeByID(db, rec.NodeID)
		if err != nil {
			return err
		}
		// 状态行存在而节点被删：直接终态，不悬置。
		if node == nil {
			if err := finishRecovery(db, rec.ID, StateFailed, "节点已删除"); err != nil {
				return err
			}
			continue
		}
		if err := advance(db, rec, *node, now, opts, reg, logger); err != nil {
			return err
		}
	}

	// 3) 判封开线：摘除持续未恢复（排除手动摘除）且无进行中流水线。
	cutoff := now.Add(-opts.SustainedAfter)
	var blocked []storage.Node
	if err := db.Where("pool_state = ? AND pool_changed_at IS NOT NULL AND pool_changed_at <= ? AND pool_reason NOT LIKE ?",
		pool.StateSuspended, cutoff, "手动%").Find(&blocked).Error; err != nil {
		return err
	}
	existing := make(map[uint]bool, len(running))
	for _, rec := range running {
		existing[rec.NodeID] = true
	}
	for _, n := range blocked {
		if existing[n.ID] {
			continue
		}
		rec := storage.Recovery{
			NodeID: n.ID, NodeName: n.Name, Level: 1, State: StateRunning,
			LevelStartedAt: now, StartedAt: now, UpdatedAt: now,
		}
		if err := db.Create(&rec).Error; err != nil {
			return err
		}
		existing[n.ID] = true
		logger.Printf("recovery open: node=%d %s（摘除持续 %s 判封，进入 L1）",
			n.ID, n.Name, opts.SustainedAfter)
	}
	return nil
}

// advance 驱动一行的当前级别：动作未开跑则启动（未注册记 skipped 推进），
// 等待中则按 LevelTimeout 超时推进；L3 耗尽记 failed。
func advance(db *gorm.DB, rec storage.Recovery, node storage.Node, now time.Time, opts Options, reg Registry, logger *log.Logger) error {
	switch rec.ActionState {
	case ActionUnset:
		a := reg[rec.Level]
		if a == nil {
			// 未注册级别（L1/L2 插件位未接）：记 skipped 直接推进。
			if err := db.Model(&storage.Recovery{}).Where("id = ?", rec.ID).
				Updates(map[string]any{"action_state": ActionSkipped, "action": "", "updated_at": now}).Error; err != nil {
				return err
			}
			logger.Printf("recovery level skipped: node=%d level=%d", rec.NodeID, rec.Level)
			return advanceNext(db, rec, now, logger)
		}
		// 同步置 running 后再入 goroutine，避免重复启动。
		if err := db.Model(&storage.Recovery{}).Where("id = ?", rec.ID).
			Updates(map[string]any{"action_state": ActionRunning, "action": a.Name(), "updated_at": now}).Error; err != nil {
			return err
		}
		go runAction(db, a, rec, node, logger)
		return nil
	case ActionSkipped, ActionFailed:
		// 未注册/动作失败：本级无望，立即进下一级。
		return advanceNext(db, rec, now, logger)
	case ActionOK:
		// 动作成功：等探测恢复；满 LevelTimeout 仍未复位进下一级。
		if now.After(rec.LevelStartedAt.Add(opts.LevelTimeout)) {
			return advanceNext(db, rec, now, logger)
		}
		return nil
	case ActionRunning:
		if !now.After(rec.LevelStartedAt.Add(opts.LevelTimeout)) {
			return nil // 等待窗口内，动作仍在跑
		}
		if err := db.Model(&storage.Recovery{}).Where("id = ?", rec.ID).
			Updates(map[string]any{"action_state": ActionFailed,
				"last_err": "超时未恢复", "updated_at": now}).Error; err != nil {
			return err
		}
		logger.Printf("recovery level timeout: node=%d level=%d", rec.NodeID, rec.Level)
		return advanceNext(db, rec, now, logger)
	}
	return nil
}

// advanceNext 推进到下一级；超过 MaxLevel 记 failed（升级人工由 BR-5 承接）。
func advanceNext(db *gorm.DB, rec storage.Recovery, now time.Time, logger *log.Logger) error {
	if rec.Level >= MaxLevel {
		return finishRecovery(db, rec.ID, StateFailed, "L1-L3 全级耗尽仍未恢复")
	}
	if err := db.Model(&storage.Recovery{}).Where("id = ?", rec.ID).
		Updates(map[string]any{"level": rec.Level + 1, "action_state": ActionUnset, "action": "",
			"level_started_at": now, "updated_at": now}).Error; err != nil {
		return err
	}
	logger.Printf("recovery advance: node=%d -> L%d", rec.NodeID, rec.Level+1)
	return nil
}

// runAction 执行一级动作并回写结果；流水线已终态则丢弃迟到结果。
func runAction(db *gorm.DB, a Action, rec storage.Recovery, node storage.Node, logger *log.Logger) {
	err := a.Run(context.Background(), node)
	updates := map[string]any{"action_state": ActionOK, "updated_at": time.Now()}
	if err != nil {
		updates["action_state"] = ActionFailed
		msg := err.Error()
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		updates["last_err"] = msg
	}
	res := db.Model(&storage.Recovery{}).
		Where("id = ? AND state = ? AND level = ?", rec.ID, StateRunning, rec.Level).
		Updates(updates)
	if res.Error != nil {
		logger.Printf("recovery action result: node=%d %v", rec.NodeID, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		return // 流水线已终态/推进，迟到结果丢弃
	}
	if err != nil {
		logger.Printf("recovery action failed: node=%d level=%d %v", rec.NodeID, rec.Level, err)
	}
}

func finishRecovery(db *gorm.DB, id uint, state, reason string) error {
	updates := map[string]any{"state": state, "finished_at": time.Now(), "updated_at": time.Now()}
	if state == StateFailed {
		updates["last_err"] = reason // 完成态不写 last_err，失败原因在此
	}
	return db.Model(&storage.Recovery{}).Where("id = ?", id).Updates(updates).Error
}

// nodeByID 查节点；不存在返回 (nil, nil)。
func nodeByID(db *gorm.DB, id uint) (*storage.Node, error) {
	var n storage.Node
	err := db.First(&n, id).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}
