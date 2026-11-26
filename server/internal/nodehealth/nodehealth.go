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

// Evaluate derives each node's health: a disabled or offline node is off; a
// pool-suspended node is degraded; a fresh sick verdict from the mutual
// probes degrades; a fresh healthy verdict with the node alive and active is
// healthy; a live node without any fresh verdict is unknown (absence of
// evidence is not health).
func Evaluate(db *gorm.DB, nodes []storage.Node, now time.Time) map[uint]string {
	ids := make([]uint, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	// Latest fresh verdict about each node (any probe kind that names it as
	// the target: tunnel and peer both speak about the node's reachability).
	verdicts := map[uint]string{}
	if len(ids) > 0 {
		var reports []storage.ProbeReport
		if err := db.Where("probed_at > ? AND target_node_id IN ?", now.Add(-ProbeFresh), ids).
			Order("probed_at ASC, id ASC").Find(&reports).Error; err == nil {
			for _, r := range reports {
				if r.TargetNodeID == nil {
					continue
				}
				verdicts[*r.TargetNodeID] = r.Verdict
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
		switch verdicts[n.ID] {
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
