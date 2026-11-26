// Package egresshealth derives per-node egress health states (F-0905) from
// the node's own exit baseline probes: whether the node can still reach the
// public internet from its own egress. It is deliberately separate from
// nodehealth (what other nodes see about this node's reachability),
// pathhealth (per-path health) and aggregate.Judge (region/ISP dimension
// failures): the exit baseline exists exactly to tell "the tunnel is broken"
// apart from "this node's own egress is broken", so it must not be folded
// into either side.
package egresshealth

import (
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Health state values, worst-last rank order for aggregation (same vocabulary
// as nodehealth/pathhealth so dash surfaces stay uniform).
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

// ProbeFresh is how long the node's own exit verdict participates in the
// decision; older counts as no verdict (the same window as the other health
// derivations).
const ProbeFresh = 30 * time.Minute

// Evaluate derives each node's egress health from its own exit baseline
// reports (the prober's view of its own public egress): a disabled or
// offline node's egress is out of service; a fresh sick exit verdict means
// the node cannot reliably reach the outside; a fresh healthy verdict is
// healthy; a live node without any fresh exit verdict is unknown -- pool
// suspension deliberately does not enter this derivation, because suspension
// only pulls the node from the entry pool while its agent and probes keep
// running (the egress keeps serving landing traffic).
func Evaluate(db *gorm.DB, nodes []storage.Node, now time.Time) map[uint]string {
	ids := make([]uint, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	// Latest fresh exit verdict per prober (last write wins).
	verdicts := map[uint]string{}
	if len(ids) > 0 {
		var reports []storage.ProbeReport
		if err := db.Where("probed_at > ? AND target_kind = ? AND node_id IN ?",
			now.Add(-ProbeFresh), agentproto.ProbeTargetExit, ids).
			Order("probed_at ASC, id ASC").Find(&reports).Error; err == nil {
			for _, r := range reports {
				verdicts[r.NodeID] = r.Verdict
			}
		}
	}

	out := make(map[uint]string, len(nodes))
	for _, n := range nodes {
		if !n.Enabled || n.Status == "offline" {
			out[n.ID] = Off
			continue
		}
		switch verdicts[n.ID] {
		case agentproto.ProbeVerdictSick:
			out[n.ID] = Degraded
		case agentproto.ProbeVerdictHealthy:
			out[n.ID] = Healthy
		default:
			out[n.ID] = Unknown
		}
	}
	return out
}
