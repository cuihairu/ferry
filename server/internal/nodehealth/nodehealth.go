// Package nodehealth derives per-node health states (F-0902) from the node's
// own signals: admin enablement, heartbeat liveness, pool state, and the
// fresh probe verdicts other nodes produced about it. It is deliberately
// separate from pathhealth (per-path availability, F-0904), the exit-baseline
// probes (egress health, F-0905) and aggregate.Judge (region/ISP dimension
// failures): one node's sickness says nothing by itself about the paths it
// carries or its whole dimension.
package nodehealth

import (
	"fmt"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Health state values, worst-last rank order for aggregation (same vocabulary
// as pathhealth so dash surfaces stay uniform).
const (
	Healthy  = "healthy"
	Unknown  = "unknown"
	Degraded = "degraded"
	Off      = "off"
)

// Rank orders the states for worst-of aggregation.
func Rank(s string) int {
	switch s {
	case Healthy:
		return 0
	case Unknown:
		return 1
	case Degraded:
		return 2
	case Off:
		return 3
	}
	return Rank(Unknown)
}

// Worst returns the more severe of two states.
func Worst(a, b string) string {
	if Rank(a) >= Rank(b) {
		return a
	}
	return b
}

// ProbeFresh is how long a verdict about the node participates in the
// decision; older counts as no verdict (the same window as the pool sweep).
const ProbeFresh = 30 * time.Minute

// verdictTally accumulates one node's fresh probe verdicts (F-0931).
type verdictTally struct {
	healthy, sick int
}

// majority is the window-majority verdict (F-0931 hysteresis): the health
// state follows the majority of the fresh window, not the single latest
// report -- one lost probe must not flap a healthy node, and one stray
// success must not whitewash a majority-sick one. Ties stay healthy: the
// burden of proof for degrading a node is a majority of sick verdicts.
func (t verdictTally) majority() string {
	switch {
	case t.sick > t.healthy:
		return agentproto.ProbeVerdictSick
	case t.healthy > 0:
		return agentproto.ProbeVerdictHealthy
	default:
		return ""
	}
}

// HealthWithReason is one node's derived health plus the explanation of
// which input decided it (F-0934): the dash surfaces the reason next to the
// state light so "why is it degraded" never needs a database dive.
type HealthWithReason struct {
	State  string
	Reason string
}

// Evaluate derives each node's health: a disabled or offline node is off; a
// pool-suspended node is degraded; a majority-sick fresh verdict window from
// the mutual probes degrades (F-0931); a majority-healthy window with the
// node alive and active is healthy; a live node without any fresh verdict is
// unknown (absence of evidence is not health). An active manual override
// (F-0932) wins over all computed states -- except off, which no override may
// whitewash.
func Evaluate(db *gorm.DB, nodes []storage.Node, now time.Time) map[uint]string {
	withReason := EvaluateWithReason(db, nodes, now)
	out := make(map[uint]string, len(nodes))
	for id, hr := range withReason {
		out[id] = hr.State
	}
	return out
}

// EvaluateWithReason is Evaluate plus the decisive-input explanation per
// node (F-0934): what flipped the state, in one line, ranked the same way
// the state itself is decided.
func EvaluateWithReason(db *gorm.DB, nodes []storage.Node, now time.Time) map[uint]HealthWithReason {
	ids := make([]uint, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	// Fresh verdicts about each node (any probe kind that names it as the
	// target: tunnel and peer both speak about the node's reachability),
	// tallied for the F-0931 window-majority verdict.
	tallies := map[uint]verdictTally{}
	if len(ids) > 0 {
		var reports []storage.ProbeReport
		if err := db.Where("probed_at > ? AND target_node_id IN ?", now.Add(-ProbeFresh), ids).
			Order("probed_at ASC, id ASC").Find(&reports).Error; err == nil {
			for _, r := range reports {
				if r.TargetNodeID == nil {
					continue
				}
				t := tallies[*r.TargetNodeID]
				switch r.Verdict {
				case agentproto.ProbeVerdictHealthy:
					t.healthy++
				case agentproto.ProbeVerdictSick:
					t.sick++
				}
				tallies[*r.TargetNodeID] = t
			}
		}
	}

	// Active manual overrides (F-0932/F-0933): expired rows simply do not
	// load, so the computed state resumes on its own.
	overrides := map[uint]storage.HealthOverride{}
	if len(ids) > 0 {
		var rows []storage.HealthOverride
		if err := db.Where("node_id IN ? AND expires_at > ?", ids, now).Find(&rows).Error; err == nil {
			for _, r := range rows {
				overrides[r.NodeID] = r
			}
		}
	}

	out := make(map[uint]HealthWithReason, len(nodes))
	for _, n := range nodes {
		switch {
		case !n.Enabled || n.Status == "offline":
			reason := "node disabled by operator"
			if n.Enabled {
				reason = "agent offline (no heartbeat)"
			}
			out[n.ID] = HealthWithReason{Off, reason}
			continue
		}
		state := Unknown
		reason := "no fresh probe verdicts (30m window)"
		if n.PoolState == "suspended" {
			state = Worst(state, Degraded)
			reason = "pool suspended"
		}
		t := tallies[n.ID]
		switch t.majority() {
		case agentproto.ProbeVerdictSick:
			state = Worst(state, Degraded)
			reason = fmt.Sprintf("probe window: %d sick vs %d healthy (30m)", t.sick, t.healthy)
			if n.PoolState == "suspended" {
				reason = "pool suspended; " + reason
			}
		case agentproto.ProbeVerdictHealthy:
			if state != Degraded {
				state = Healthy
			}
			probes := fmt.Sprintf("probe window: %d healthy vs %d sick (30m)", t.healthy, t.sick)
			reason = probes
			if state == Degraded {
				reason = "pool suspended; " + probes
			}
		}
		// The operator's explicit verdict outranks the derivation (F-0932):
		// forced healthy lifts a suspended/degraded node, forced degraded
		// pins a flapping one. The reason names the override so the forced
		// state is never mistaken for a computed one (F-0934).
		if o, ok := overrides[n.ID]; ok {
			state = o.State
			reason = fmt.Sprintf("manual override by %s until %s: %s",
				o.Operator, o.ExpiresAt.Format("2006-01-02 15:04 MST"), o.Reason)
		}
		out[n.ID] = HealthWithReason{state, reason}
	}
	return out
}
