// Package recovery 是封禁恢复流水线（BR-1）：判封状态机与 L1→L2→L3
// 分级编排。判封口径：入口被 E-16 摘除后持续 SustainedAfter 仍未复位
// （探测仍不可达）→ 判封入流水线；探测恢复（摘挂复位）随时完成。
// 级别动作经 Registry 注册，未注册级别记 skipped 直接推进：
// L1 域名前置 DNS 切换（BR-2 插件位）、L2 IP 池补位（BR-3 插件位）、
// L3 一键开新机（接 §1 供给流水线）。每级超时未恢复进下一级，
// 全级耗尽记 failed 并告警升级人工（BR-5，经通知通道外发）；
// 每级尝试落 recovery_actions 留痕供回放。
package recovery

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 状态与级别取值。
const (
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"

	// ActionUnset/ActionRunning 等动作态取值（Recovery.ActionState）；
	// ActionTimeout 只出现在留痕行（RecoveryAction.State，BR-5）。
	ActionUnset   = ""
	ActionRunning = "running"
	ActionOK      = "ok"
	ActionFailed  = "failed"
	ActionSkipped = "skipped"
	ActionTimeout = "timeout"

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
	// OnDone 是恢复完成（探测恢复收尾，failed 不触发）的钩子，主装配
	// 注入（BR-2：域名前置回切常态 IP）。同步执行——done 每轮至多几条，
	// 下游通道自带超时，测试据此可确定断言。
	OnDone func(nodeID uint, nodeName string)
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
			if err := finishRecovery(db, rec, StateDone, "探测恢复，摘挂复位"); err != nil {
				return err
			}
			logger.Printf("recovery done: node=%d %s", rec.NodeID, rec.NodeName)
			if opts.OnDone != nil {
				opts.OnDone(rec.NodeID, rec.NodeName)
			}
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
			if err := finishRecovery(db, rec, StateFailed, "节点已删除"); err != nil {
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
		// 判封事件（HERALD-3）：单节点封禁告警；恢复完成不发（dash 流水线
		// 留痕可见）。失败只记日志不阻断判封。
		if _, err := herald.Emit(db, herald.EmitInput{
			Kind: herald.KindNodeBlocked, Severity: herald.SeverityCritical,
			Title:    fmt.Sprintf("节点 %s 判封，进入恢复流水线", n.Name),
			Body:     fmt.Sprintf("摘除持续 %s 未复位，L1→L3 分级自动恢复已启动", opts.SustainedAfter),
			Target:   herald.TargetAdmin,
			DedupKey: fmt.Sprintf("node:%d:node_blocked", n.ID),
			Meta:     map[string]any{"node_id": n.ID, "reason": n.PoolReason},
		}); err != nil {
			logger.Printf("herald emit node_blocked: node=%d %v", n.ID, err)
		}
	}
	return nil
}

// advance 驱动一行的当前级别：动作未开跑则启动（未注册记 skipped 推进），
// 等待中则按 LevelTimeout 超时推进；L3 耗尽记 failed。每级尝试同步落
// recovery_actions 留痕（BR-5）。
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
			traceRecovery(db, rec, storage.RecoveryAction{
				RecoveryID: rec.ID, NodeID: rec.NodeID, NodeName: rec.NodeName,
				Level: rec.Level, State: ActionSkipped, Detail: "级别未注册",
				StartedAt: now, UpdatedAt: now, FinishedAt: &now,
			}, logger)
			logger.Printf("recovery level skipped: node=%d level=%d", rec.NodeID, rec.Level)
			return advanceNext(db, rec, now, logger)
		}
		// 同步置 running 后再入 goroutine，避免重复启动。
		if err := db.Model(&storage.Recovery{}).Where("id = ?", rec.ID).
			Updates(map[string]any{"action_state": ActionRunning, "action": a.Name(), "updated_at": now}).Error; err != nil {
			return err
		}
		traceRecovery(db, rec, storage.RecoveryAction{
			RecoveryID: rec.ID, NodeID: rec.NodeID, NodeName: rec.NodeName,
			Level: rec.Level, Action: a.Name(), State: ActionRunning,
			StartedAt: now, UpdatedAt: now,
		}, logger)
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
		finishTrace(db, rec, ActionTimeout, "超时未恢复", now, logger)
		logger.Printf("recovery level timeout: node=%d level=%d", rec.NodeID, rec.Level)
		return advanceNext(db, rec, now, logger)
	}
	return nil
}

// advanceNext 推进到下一级；超过 MaxLevel 记 failed 并告警升级人工（BR-5）。
func advanceNext(db *gorm.DB, rec storage.Recovery, now time.Time, logger *log.Logger) error {
	if rec.Level >= MaxLevel {
		return finishRecovery(db, rec, StateFailed, "L1-L3 全级耗尽仍未恢复")
	}
	if err := db.Model(&storage.Recovery{}).Where("id = ?", rec.ID).
		Updates(map[string]any{"level": rec.Level + 1, "action_state": ActionUnset, "action": "",
			"level_started_at": now, "updated_at": now}).Error; err != nil {
		return err
	}
	logger.Printf("recovery advance: node=%d -> L%d", rec.NodeID, rec.Level+1)
	return nil
}

// runAction 执行一级动作并回写结果；流水线已终态则丢弃迟到结果
// （留痕行按 recovery+level+running 收尾，同样天然丢弃）。
func runAction(db *gorm.DB, a Action, rec storage.Recovery, node storage.Node, logger *log.Logger) {
	err := a.Run(context.Background(), node)
	now := time.Now()
	updates := map[string]any{"action_state": ActionOK, "updated_at": now}
	traceState, detail := ActionOK, ""
	if err != nil {
		updates["action_state"] = ActionFailed
		msg := err.Error()
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		updates["last_err"] = msg
		traceState, detail = ActionFailed, msg
	}
	res := db.Model(&storage.Recovery{}).
		Where("id = ? AND state = ? AND level = ?", rec.ID, StateRunning, rec.Level).
		Updates(updates)
	if res.Error != nil {
		logger.Printf("recovery action result: node=%d %v", rec.NodeID, res.Error)
		return
	}
	finishTrace(db, rec, traceState, detail, now, logger)
	if res.RowsAffected == 0 {
		return // 流水线已终态/推进，迟到结果丢弃
	}
	if err != nil {
		logger.Printf("recovery action failed: node=%d level=%d %v", rec.NodeID, rec.Level, err)
	}
}

// traceRecovery 落一条动作留痕行（BR-5）；留痕失败只记日志不阻断流水线。
func traceRecovery(db *gorm.DB, rec storage.Recovery, row storage.RecoveryAction, logger *log.Logger) {
	if err := db.Create(&row).Error; err != nil {
		logger.Printf("recovery trace: node=%d %v", rec.NodeID, err)
	}
}

// finishTrace 收尾当前级别在途的留痕行（running → 终态）；无在途行忽略。
func finishTrace(db *gorm.DB, rec storage.Recovery, state, detail string, now time.Time, logger *log.Logger) {
	updates := map[string]any{"state": state, "finished_at": now, "updated_at": now}
	if detail != "" {
		updates["detail"] = detail
	}
	res := db.Model(&storage.RecoveryAction{}).
		Where("recovery_id = ? AND level = ? AND state = ?", rec.ID, rec.Level, ActionRunning).
		Updates(updates)
	if res.Error != nil {
		logger.Printf("recovery trace finish: node=%d %v", rec.NodeID, res.Error)
	}
}

// finishRecovery 收尾流水线：failed 翻转时经通知通道外发升级人工
// （BR-5；现通道 Webhook，HERALD 落地后统一改投同一接口位）。
func finishRecovery(db *gorm.DB, rec storage.Recovery, state, reason string) error {
	now := time.Now()
	updates := map[string]any{"state": state, "finished_at": now, "updated_at": now}
	if state == StateFailed {
		updates["last_err"] = reason // 完成态不写 last_err，失败原因在此
	}
	if err := db.Model(&storage.Recovery{}).Where("id = ?", rec.ID).Updates(updates).Error; err != nil {
		return err
	}
	if state == StateFailed {
		// 升级人工（BR-5）：落事件 outbox 统一投递（HERALD-3），webhook
		// 通道并行期保留，Herald 验证后收敛。
		if _, err := herald.Emit(db, herald.EmitInput{
			Kind: herald.KindRecoveryFailed, Severity: herald.SeverityCritical,
			Title:    fmt.Sprintf("节点 %s 恢复流水线失败", rec.NodeName),
			Body:     reason + "，需人工介入",
			Target:   herald.TargetAdmin,
			DedupKey: fmt.Sprintf("recovery:%d:recovery_failed", rec.ID),
			Meta:     map[string]any{"recovery_id": rec.ID, "node_id": rec.NodeID, "level": rec.Level},
		}); err != nil {
			log.Printf("recovery emit recovery_failed: node=%d %v", rec.NodeID, err)
		}
		if n := notify.FromDB(db); n.Enabled() {
			_ = n.Send(notify.Event{
				Event: "recovery_failed",
				Text: fmt.Sprintf("节点 %s 恢复流水线失败：%s，需人工介入",
					rec.NodeName, reason),
				Fields: map[string]any{
					"recovery_id": rec.ID, "node_id": rec.NodeID,
					"node_name": rec.NodeName, "level": rec.Level, "reason": reason,
				},
			})
		}
	}
	return nil
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
