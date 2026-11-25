// Package pool implements automatic entry-pool suspend/resume (E-16): SickStrikes consecutive
// sick edge-mutual/tunnel probe verdicts → suspend (pool_state=suspended, gone from the
// subscribed entry pool at once); WellStreak consecutive healthy → resume. Suspend/resume
// never touches enabled — the agent connection and probing stay, giving the resume verdict its
// basis. Subscriptions consume the pool directly (active entries fill in); landing reassignment (config.push) arrives with the E-21 allocation policy.
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

// Default verdict thresholds and probe-result freshness.
const (
	// DefaultSickStrikes is the consecutive-sick threshold for suspension.
	DefaultSickStrikes = 3
	// DefaultWellStreak is the consecutive-healthy threshold for resumption.
	DefaultWellStreak = 2
	// ProbeFresh is the time limit for a verdict to count; older ones count as no verdict.
	ProbeFresh = 30 * time.Minute
)

// Pool state values.
const (
	StateActive    = "active"
	StateSuspended = "suspended"
)

// Options holds the suspend/resume thresholds; the zero value falls back to defaults.
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

// Event is one suspend/resume state transition.
type Event struct {
	NodeID uint
	Name   string
	Action string // suspend / resume / manual_resume
	Reason string
}

// OnChange is an in-process hook on suspend/resume transitions (the event-driven hook point
// for E-27 per-region reconciliation; design §C.8, no polling): called after sweepOnce produces
// a transition or a manual suspend/resume succeeds (wired once in main); nil-safe, async, never blocks the main flow.
var OnChange func(db *gorm.DB)

// ErrNotSuspended is returned by manual resume when the node is not suspended.
var ErrNotSuspended = errors.New("node not suspended")

// ErrAlreadySuspended is returned by manual suspend when the node is already suspended.
var ErrAlreadySuspended = errors.New("node already suspended")

// Loop runs the suspend/resume verdict periodically until ctx is cancelled; transitions are logged and announced.
func Loop(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTimer(0) // run one round immediately on start
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
		// A planned maintenance window (F-0206) still tracks health but does
		// not page anyone: the operator already knows the node is out.
		var n storage.Node
		if err := db.Select("maintenance").First(&n, ev.NodeID).Error; err != nil {
			logger.Printf("read node %d maintenance: %v", ev.NodeID, err)
			continue
		}
		if n.Maintenance {
			continue
		}
		announce(db, logger, ev)
		// Single-node suspend/resume events (HERALD-3 slack): only automatic suspension emits node_down;
		// resume and manual actions do not (recovery is instant on the subscription side, no alert reach needed). Failures only log.
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

// Sweep runs one round of suspend/resume verdicts: for each entry/dual-role node, take the most
// recent K edge-mutual and tunnel verdicts in the window (K=max(SickStrikes, WellStreak), newest
// first); the most recent SickStrikes all sick → suspend; while suspended, the most recent
// WellStreak all healthy → resume. Returns the state-transition events.
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
				continue // already suspended; no repeated transition
			}
			reason := fmt.Sprintf("连续 %d 次探测 sick", opts.SickStrikes)
			if err := setPoolState(db, n.ID, StateSuspended, reason); err != nil {
				return events, err
			}
			events = append(events, Event{NodeID: n.ID, Name: n.Name, Action: "suspend", Reason: reason})
		case len(verdicts) >= opts.WellStreak && allVerdicts(verdicts[:opts.WellStreak], agentproto.ProbeVerdictHealthy):
			if n.PoolState != StateSuspended {
				continue // not suspended; nothing to resume
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

// Resume is a manual reset (admin side): a suspended node is lifted back, a standby node returns to the pool; same trace and notifications.
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
		// Standby return: leaves the standby list straight into subscriptions.
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

// Suspend is a manual removal (E-25 admin side): out of the pool at once (immediate for
// subscriptions) with trace and notification; resuming still goes through the automatic verdict or manual resume. ErrAlreadySuspended when already suspended.
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

// allVerdicts reports whether the verdict slice is entirely the given verdict (an empty slice is not).
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

// setPoolState persists the pool state and the transition trace.
func setPoolState(db *gorm.DB, nodeID uint, state, reason string) error {
	return db.Model(&storage.Node{}).Where("id = ?", nodeID).
		Updates(map[string]any{
			"pool_state":      state,
			"pool_changed_at": time.Now(),
			"pool_reason":     reason,
		}).Error
}

// announce sends state-transition events outbound (P1-10 notify channel); failures only log and never block.
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
