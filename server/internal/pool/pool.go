// Package pool 入口池自动摘挂（E-16）：边缘互探/隧道探测结论连续
// SickStrikes 次 sick → 摘除（pool_state=suspended，订阅入口池即时消失），
// 恢复连续 WellStreak 次 healthy → 复位。摘挂不动 enabled——agent 连接
// 与探测保持，复位判定才有依据。订阅是入口池的直接消费方（剩余 active
// 入口自然补位）；落地换线（config.push 变更列表）随 E-21 分配策略接入。
package pool

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 判定口径默认值与探测结论新鲜度。
const (
	// DefaultSickStrikes 连续 sick 摘除阈值。
	DefaultSickStrikes = 3
	// DefaultWellStreak 连续 healthy 复位阈值。
	DefaultWellStreak = 2
	// ProbeFresh 探测结论参与判定的时限，超龄视为无结论。
	ProbeFresh = 30 * time.Minute
)

// 池状态取值。
const (
	StateActive    = "active"
	StateSuspended = "suspended"
)

// Options 是摘挂口径，零值回退默认。
type Options struct {
	SickStrikes int
	WellStreak  int
}

func (o Options) normalize() Options {
	if o.SickStrikes <= 0 {
		o.SickStrikes = DefaultSickStrikes
	}
	if o.WellStreak <= 0 {
		o.WellStreak = DefaultWellStreak
	}
	return o
}

// Event 是一次摘挂状态迁移。
type Event struct {
	NodeID uint
	Name   string
	Action string // suspend / resume / manual_resume
	Reason string
}

// OnChange 摘挂状态迁移的进程内钩子（E-27 分地域对账的事件驱动挂点，
// 设计 §C.8 不建轮询）：sweepOnce 产生迁移或手动摘挂成功后回调（同进程
// main 装配一次）；nil 安全，异步触发不阻断摘挂主流程。
var OnChange func(db *gorm.DB)

// ErrNotSuspended 手动复位时节点不在摘除状态。
var ErrNotSuspended = errors.New("node not suspended")

// ErrAlreadySuspended 手动摘除时节点已在摘除状态。
var ErrAlreadySuspended = errors.New("node already suspended")

// Loop 周期执行摘挂判定直到 ctx 取消；状态迁移记日志并外发事件。
func Loop(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTimer(0) // 启动即跑一轮
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := sweepOnce(db, logger); err != nil {
			logger.Printf("pool sweep: %v", err)
		}
		t.Reset(interval)
	}
}

func sweepOnce(db *gorm.DB, logger *log.Logger) error {
	events, err := Sweep(db, Options{})
	if err != nil {
		return err
	}
	for _, ev := range events {
		logger.Printf("pool %s: node %d %s（%s）", ev.Action, ev.NodeID, ev.Name, ev.Reason)
		announce(db, logger, ev)
		// 单点摘挂事件（HERALD-3 余量）：仅自动摘除发 node_down，复位/
		// 手动动作不发（恢复在订阅侧即时生效，无需告警触达）。失败只记日志。
		if ev.Action == "suspend" {
			if _, err := herald.Emit(db, herald.EmitInput{
				Kind: herald.KindNodeDown, Severity: herald.SeverityWarning,
				Title:    fmt.Sprintf("节点 %s 已从入口池摘除", ev.Name),
				Body:     ev.Reason + "，订阅入口池即时生效",
				Target:   herald.TargetAdmin,
				DedupKey: fmt.Sprintf("node:%d:node_down", ev.NodeID),
				Meta:     map[string]any{"node_id": ev.NodeID, "reason": ev.Reason},
			}); err != nil {
				logger.Printf("herald emit node_down: node=%d %v", ev.NodeID, err)
			}
		}
	}
	if len(events) > 0 && OnChange != nil {
		OnChange(db)
	}
	return nil
}

// Sweep 执行一轮摘挂判定：每个入口/双角色节点取窗口内最近 K 条
// 互探与隧道结论（K=max(SickStrikes, WellStreak)，新在前），最近
// SickStrikes 条全 sick → 摘除；摘除态下最近 WellStreak 条全 healthy
// → 复位。返回状态迁移事件。
func Sweep(db *gorm.DB, opts Options) ([]Event, error) {
	opts = opts.normalize()
	keep := opts.SickStrikes
	if opts.WellStreak > keep {
		keep = opts.WellStreak
	}
	var nodes []storage.Node
	if err := db.Where("enabled = ? AND role IN (?, ?)", true, "entry", "both").
		Find(&nodes).Error; err != nil {
		return nil, err
	}
	since := time.Now().Add(-ProbeFresh)
	var events []Event
	for _, n := range nodes {
		var verdicts []string
		if err := db.Model(&storage.ProbeReport{}).
			Where("target_node_id = ? AND target_kind IN ? AND probed_at > ?",
				n.ID, []string{agentproto.ProbeTargetTunnel, agentproto.ProbeTargetPeer}, since).
			Order("probed_at DESC, id DESC").Limit(keep).
			Pluck("verdict", &verdicts).Error; err != nil {
			return events, err
		}
		switch {
		case len(verdicts) >= opts.SickStrikes && allVerdicts(verdicts[:opts.SickStrikes], agentproto.ProbeVerdictSick):
			if n.PoolState == StateSuspended {
				continue // 已摘除，不重复迁移
			}
			reason := fmt.Sprintf("连续 %d 次探测 sick", opts.SickStrikes)
			if err := setPoolState(db, n.ID, StateSuspended, reason); err != nil {
				return events, err
			}
			events = append(events, Event{NodeID: n.ID, Name: n.Name, Action: "suspend", Reason: reason})
		case len(verdicts) >= opts.WellStreak && allVerdicts(verdicts[:opts.WellStreak], agentproto.ProbeVerdictHealthy):
			if n.PoolState != StateSuspended {
				continue // 未摘除无需复位
			}
			reason := fmt.Sprintf("连续 %d 次探测 healthy", opts.WellStreak)
			if err := setPoolState(db, n.ID, StateActive, reason); err != nil {
				return events, err
			}
			events = append(events, Event{NodeID: n.ID, Name: n.Name, Action: "resume", Reason: reason})
		}
	}
	return events, nil
}

// Resume 手动复位（管理端）：摘除态人工摘回、预备态回池，走同一留痕与通知。
func Resume(db *gorm.DB, nodeID uint, logger *log.Logger) (storage.Node, error) {
	if logger == nil {
		logger = log.Default()
	}
	var n storage.Node
	if err := db.First(&n, nodeID).Error; err != nil {
		return storage.Node{}, err
	}
	switch n.PoolState {
	case StateSuspended:
	case StateStandby:
		// 预备回池：出待命清单直接入订阅。
	default:
		return storage.Node{}, ErrNotSuspended
	}
	reason := "手动复位"
	if n.PoolState == StateStandby {
		reason = "预备回池"
	}
	if err := setPoolState(db, n.ID, StateActive, reason); err != nil {
		return storage.Node{}, err
	}
	logger.Printf("pool manual_resume: node %d %s", n.ID, n.Name)
	announce(db, logger, Event{NodeID: n.ID, Name: n.Name, Action: "manual_resume", Reason: reason})
	n.PoolState, n.PoolReason = StateActive, reason
	now := time.Now()
	n.PoolChangedAt = &now
	return n, nil
}

// Suspend 手动摘除（E-25 管理端）：立即出池（订阅即时生效）并留痕通知；
// 摘除态复位仍走自动判定或手动复位。已在摘除态报 ErrAlreadySuspended。
func Suspend(db *gorm.DB, nodeID uint, logger *log.Logger) (storage.Node, error) {
	if logger == nil {
		logger = log.Default()
	}
	var n storage.Node
	if err := db.First(&n, nodeID).Error; err != nil {
		return storage.Node{}, err
	}
	if n.Role != "entry" && n.Role != "both" {
		return storage.Node{}, errors.New("node is not an entry")
	}
	if n.PoolState == StateSuspended {
		return storage.Node{}, ErrAlreadySuspended
	}
	reason := "手动摘除"
	if err := setPoolState(db, n.ID, StateSuspended, reason); err != nil {
		return storage.Node{}, err
	}
	logger.Printf("pool manual_suspend: node %d %s", n.ID, n.Name)
	announce(db, logger, Event{NodeID: n.ID, Name: n.Name, Action: "manual_suspend", Reason: reason})
	n.PoolState, n.PoolReason = StateSuspended, reason
	now := time.Now()
	n.PoolChangedAt = &now
	return n, nil
}

// allVerdicts 结论序列是否全为指定判定（空序列不算）。
func allVerdicts(verdicts []string, want string) bool {
	if len(verdicts) == 0 {
		return false
	}
	for _, v := range verdicts {
		if v != want {
			return false
		}
	}
	return true
}

// setPoolState 落池状态与迁移留痕。
func setPoolState(db *gorm.DB, nodeID uint, state, reason string) error {
	return db.Model(&storage.Node{}).Where("id = ?", nodeID).
		Updates(map[string]any{
			"pool_state":      state,
			"pool_changed_at": time.Now(),
			"pool_reason":     reason,
		}).Error
}

// announce 状态迁移外发事件（P1-10 通知通道）；失败只记日志不阻断。
func announce(db *gorm.DB, logger *log.Logger, ev Event) {
	n := notify.FromDB(db)
	if !n.Enabled() {
		return
	}
	e := notify.Event{
		Event:  "pool." + ev.Action,
		Text:   fmt.Sprintf("入口池 %s：节点 %s（%s）", ev.Action, ev.Name, ev.Reason),
		Fields: map[string]any{"node_id": ev.NodeID, "action": ev.Action, "reason": ev.Reason},
	}
	if err := n.Send(e); err != nil {
		logger.Printf("notify webhook: %v", err)
	}
}
