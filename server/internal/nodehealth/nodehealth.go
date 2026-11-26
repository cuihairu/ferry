// Package nodehealth derives per-node health states (F-0902) from the node's
// own signals: admin enablement, heartbeat liveness, pool state, and the
// fresh probe verdicts other nodes produced about it. It is deliberately
// separate from pathhealth (per-path availability, F-0904), the exit-baseline
// probes (egress health, F-0905) and aggregate.Judge (region/ISP dimension
// failures): one node's sickness says nothing by itself about the paths it
// carries or its whole dimension.
package nodehealth

import (
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

// Evaluate derives each node's health: a disabled or offline node is off; a
// pool-suspended node is degraded; a majority-sick fresh verdict window from
// the mutual probes degrades (F-0931); a majority-healthy window with the
// node alive and active is healthy; a live node without any fresh verdict is
// unknown (absence of evidence is not health).
func Evaluate(db *gorm.DB, nodes []storage.Node, now time.Time) map[uint]string {
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

	out := make(map[uint]string, len(nodes))
	for _, n := range nodes {
		switch {
		case !n.Enabled || n.Status == "offline":
			out[n.ID] = Off
			continue
		}
		state := Unknown
		if n.PoolState == "suspended" {
			state = Worst(state, Degraded)
		}
		switch tallies[n.ID].majority() {
		case agentproto.ProbeVerdictSick:
			state = Worst(state, Degraded)
		case agentproto.ProbeVerdictHealthy:
			if state != Degraded {
				state = Healthy
			}
		}
		out[n.ID] = state
	}
	return out
}
