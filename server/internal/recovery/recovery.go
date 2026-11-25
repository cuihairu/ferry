// Package recovery implements the block-recovery pipeline (BR-1): the block
// verdict state machine and tiered L1→L2→L3 orchestration. Verdict rule: an
// entry removed by E-16 that stays unrestored for SustainedAfter (probe still
// unreachable) enters the pipeline; probe recovery (reset) finishes it anytime.
// Level actions register via the Registry; unregistered levels record skipped
// and advance directly: L1 domain-front DNS switch (BR-2 slot), L2 IP pool
// backup (BR-3 slot), L3 one-click new instance (§1 provisioning). Timeout
// advances a level; exhaustion fails + escalates (BR-5, notify channel); attempts traced to recovery_actions.
package recovery

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/safestr"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// State and level values.
const (
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"

	// ActionUnset/ActionRunning etc. are the action-state values (Recovery.ActionState);
	// ActionTimeout only appears on trace rows (RecoveryAction.State, BR-5).
	ActionUnset   = ""
	ActionRunning = "running"
	ActionOK      = "ok"
	ActionFailed  = "failed"
	ActionSkipped = "skipped"
	ActionTimeout = "timeout"

	// MaxLevel is the three-level recovery ceiling (L1 domain/DNS → L2 IP pool → L3 new instance).
	MaxLevel = 3
)

// Default verdict and advancement rules.
const (
	DefaultSustainedAfter = 10 * time.Minute
	DefaultLevelTimeout   = 10 * time.Minute
)

// Options holds the verdict/advancement rules; zero values fall back to defaults.
type Options struct {
	// SustainedAfter is how long an entry must stay unrestored after removal before
	// verdict (leaves a window for probe recovery, avoiding a new instance on a blip).
	SustainedAfter time.Duration
	// LevelTimeout is the wait window for a single-level action: timeout without recovery advances.
	LevelTimeout time.Duration
	// OnDone is the hook fired when recovery completes (probe-recovery finish;
	// failed does not trigger it), injected by the main assembly (BR-2: domain front
	// switch back to the normal IP). Runs synchronously — done has only a few rows per round and downstream channels carry their own timeouts, so tests can assert deterministically.
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

// Action is one recovery-level action. Run may block for minutes (L3 new
// instance); the engine runs it in a goroutine, with the result written back
type Action interface {
	Name() string
	Run(ctx context.Context, node storage.Node) error
}

// Registry maps levels (1-3) to actions; absent levels advance as skipped.
type Registry map[int]Action

// Loop is the state machine's periodic loop (interval 0 falls back to 60s).
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

// Sweep runs one state-machine round: finish recovering rows, advance
// waiting rows, and open new verdicts. Single-threaded serial (engine level);
func Sweep(db *gorm.DB, now time.Time, opts Options, reg Registry, logger *log.Logger) error {
	if logger == nil {
		logger = log.Default()
	}
	opts = opts.normalize()
	if reg == nil {
		reg = Registry{}
	}

	// 1) Probe recovery: suspension reset (pool_state=active) means done; collect first, unconditionally.
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
		// Re-fetch: the step above already collected recovering rows, so what remains waits to advance.
		if err := db.Where("state = ?", StateRunning).Find(&running).Error; err != nil {
			return err
		}
	}

	// 2) Advance: drive the current level per row (start action / timeout into next / exhaust and fail).
	for _, rec := range running {
		node, err := nodeByID(db, rec.NodeID)
		if err != nil {
			return err
		}
		// State row exists but the node was deleted: go straight to terminal, do not hang.
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

	// 3) Open verdicts: removal unrestored for a while (excluding manual removal) and no running pipeline.
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
		// Verdict event (HERALD-3): single-node block alert; no alert on recovery
		// completion (visible in the dash pipeline traces). Failures only log, never block verdict.
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

// advance drives a row's current level: start the action if not running (unregistered
// records skipped and advances), advance on timeout while waiting; L3 exhaustion
// records failed. Each attempt synchronously writes a recovery_actions trace (BR-5).
func advance(db *gorm.DB, rec storage.Recovery, node storage.Node, now time.Time, opts Options, reg Registry, logger *log.Logger) error {
	switch rec.ActionState {
	case ActionUnset:
		a := reg[rec.Level]
		if a == nil {
			// Unregistered level (L1/L2 plugin slot not wired): record skipped and advance.
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
		// Set running synchronously before the goroutine starts, to avoid double-launch.
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
		// Unregistered/failed action: this level is hopeless, advance immediately.
		return advanceNext(db, rec, now, logger)
	case ActionOK:
		// Action succeeded: wait for probe recovery; advance if still unrestored past LevelTimeout.
		if now.After(rec.LevelStartedAt.Add(opts.LevelTimeout)) {
			return advanceNext(db, rec, now, logger)
		}
		return nil
	case ActionRunning:
		if !now.After(rec.LevelStartedAt.Add(opts.LevelTimeout)) {
			return nil // within the wait window, the action is still running
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

// advanceNext moves to the next level; past MaxLevel records failed and
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

// runAction runs one level action and writes back the result; late results are dropped
// when the pipeline is already terminal (trace rows finish by recovery+level+running, dropped too).
func runAction(db *gorm.DB, a Action, rec storage.Recovery, node storage.Node, logger *log.Logger) {
	err := a.Run(context.Background(), node)
	now := time.Now()
	updates := map[string]any{"action_state": ActionOK, "updated_at": now}
	traceState, detail := ActionOK, ""
	if err != nil {
		updates["action_state"] = ActionFailed
		msg := err.Error()
		if len(msg) > 500 {
			msg = safestr.Tail(msg, 500)
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
		return // pipeline already terminal/advanced, late result dropped
	}
	if err != nil {
		logger.Printf("recovery action failed: node=%d level=%d %v", rec.NodeID, rec.Level, err)
	}
}

// traceRecovery writes one action trace row (BR-5); trace failures only
func traceRecovery(db *gorm.DB, rec storage.Recovery, row storage.RecoveryAction, logger *log.Logger) {
	if err := db.Create(&row).Error; err != nil {
		logger.Printf("recovery trace: node=%d %v", rec.NodeID, err)
	}
}

// finishTrace closes out the in-flight trace row of the current level
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

// finishRecovery closes the pipeline: on the failed transition it sends an
// escalation to a human over the notify channel (BR-5; the current channel is
func finishRecovery(db *gorm.DB, rec storage.Recovery, state, reason string) error {
	now := time.Now()
	updates := map[string]any{"state": state, "finished_at": now, "updated_at": now}
	if state == StateFailed {
		updates["last_err"] = reason // the done state does not write last_err; the failure reason lives here
	}
	if err := db.Model(&storage.Recovery{}).Where("id = ?", rec.ID).Updates(updates).Error; err != nil {
		return err
	}
	if state == StateFailed {
		// Escalate to a human (BR-5): drop an event into the outbox for unified
		// delivery (HERALD-3); the webhook channel is kept during the parallel
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

// nodeByID looks up a node; missing returns (nil, nil).
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
