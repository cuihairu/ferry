// Package pathhealth derives per-path and per-group health states (F-0251/
// F-0268/F-0269) from the existing runtime signals: fresh probe verdicts on
// the landing node, endpoint node online/pool states, and the path enabled
// flag. Health is computed on read -- there is no stored column to keep in
// sync, so the state is always as fresh as the underlying signals.
package pathhealth

import (
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Health state values, worst-last rank order for aggregation.
const (
	Healthy  = "healthy"
	Unknown  = "unknown"
	Degraded = "degraded"
	Off      = "off"
)

// ProbeFresh is how long a probe verdict participates in the decision; older
// counts as no verdict (same window as the allocation sweep).
const ProbeFresh = 30 * time.Minute

// AvailabilityWindow is the lookback for group availability statistics (F-0269).
const AvailabilityWindow = 24 * time.Hour

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

// Evaluate derives the health state of each path: disabled is off; an
// offline or pool-suspended endpoint, or a fresh sick probe on the landing,
// is degraded; a fresh healthy probe with all endpoints up is healthy; no
// fresh verdict at all (nothing probing this path yet) is unknown.
func Evaluate(db *gorm.DB, paths []storage.Path, now time.Time) map[uint]string {
	// Endpoint node rows: entry + landing + relay hop.
	ids := map[uint]bool{}
	for _, p := range paths {
		ids[p.EntryNodeID] = true
		ids[p.LandingNodeID] = true
		if p.RelayNodeID != nil {
			ids[*p.RelayNodeID] = true
		}
	}
	nodes := map[uint]storage.Node{}
	if len(ids) > 0 {
		keys := make([]uint, 0, len(ids))
		for id := range ids {
			keys = append(keys, id)
		}
		var rows []storage.Node
		if err := db.Where("id IN ?", keys).Find(&rows).Error; err == nil {
			for _, n := range rows {
				nodes[n.ID] = n
			}
		}
	}
	// Latest fresh probe verdict per landing node (last write wins).
	verdicts := map[uint]string{}
	var reports []storage.ProbeReport
	if err := db.Where("probed_at > ? AND target_kind IN ? AND target_node_id IS NOT NULL",
		now.Add(-ProbeFresh), []string{agentproto.ProbeTargetTunnel, agentproto.ProbeTargetPeer}).
		Order("probed_at ASC, id ASC").Find(&reports).Error; err == nil {
		for _, r := range reports {
			verdicts[*r.TargetNodeID] = r.Verdict
		}
	}

	out := make(map[uint]string, len(paths))
	for _, p := range paths {
		if !p.Enabled {
			out[p.ID] = Off
			continue
		}
		state := Healthy
		for _, nid := range []uint{p.EntryNodeID, p.LandingNodeID} {
			n, ok := nodes[nid]
			if !ok {
				state = Worst(state, Degraded) // endpoint row gone: cannot carry traffic
				continue
			}
			if n.Status == "offline" || n.PoolState == "suspended" {
				state = Worst(state, Degraded)
			}
		}
		if p.RelayNodeID != nil {
			if n, ok := nodes[*p.RelayNodeID]; ok &&
				(n.Status == "offline" || n.PoolState == "suspended") {
				state = Worst(state, Degraded)
			}
		}
		if v, ok := verdicts[p.LandingNodeID]; ok {
			if v == agentproto.ProbeVerdictSick {
				state = Worst(state, Degraded)
			}
		} else {
			state = Worst(state, Unknown)
		}
		out[p.ID] = state
	}
	return out
}

// MemberHealth is one group member's resolved health (F-0268 API payload).
type MemberHealth struct {
	PathID uint   `json:"path_id"`
	Health string `json:"health"`
}

// GroupHealth aggregates a group's active (non-excluded) member paths by
// worst-of; a group with no active members is unknown.
func GroupHealth(db *gorm.DB, groupID uint, now time.Time) (string, []MemberHealth, error) {
	var members []storage.PathGroupMember
	if err := db.Where("group_id = ? AND excluded = ?", groupID, false).
		Order("position ASC").Find(&members).Error; err != nil {
		return Unknown, nil, err
	}
	ids := make([]uint, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.PathID)
	}
	var paths []storage.Path
	if len(ids) > 0 {
		if err := db.Where("id IN ?", ids).Find(&paths).Error; err != nil {
			return Unknown, nil, err
		}
	}
	byPath := Evaluate(db, paths, now)
	out := make([]MemberHealth, 0, len(paths))
	if len(paths) == 0 {
		return Unknown, out, nil
	}
	// Aggregate over the actual members; the neutral start is healthy, so a
	// lone healthy member is not stuck at the empty-group unknown.
	state := Healthy
	for _, p := range paths {
		s := byPath[p.ID]
		state = Worst(state, s)
		out = append(out, MemberHealth{PathID: p.ID, Health: s})
	}
	return state, out, nil
}

// Availability returns the group's availability percentage over the stats
// window (F-0269): the share of healthy probe verdicts across the member
// paths' landing nodes. Nil when there is no probe data at all.
func Availability(db *gorm.DB, groupID uint, now time.Time) (*float64, error) {
	var members []storage.PathGroupMember
	if err := db.Where("group_id = ? AND excluded = ?", groupID, false).
		Find(&members).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.PathID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var paths []storage.Path
	if err := db.Where("id IN ?", ids).Find(&paths).Error; err != nil {
		return nil, err
	}
	landings := map[uint]bool{}
	for _, p := range paths {
		landings[p.LandingNodeID] = true
	}
	if len(landings) == 0 {
		return nil, nil
	}
	keys := make([]uint, 0, len(landings))
	for id := range landings {
		keys = append(keys, id)
	}
	var reports []storage.ProbeReport
	if err := db.Where("probed_at > ? AND target_kind IN ? AND target_node_id IN ?",
		now.Add(-AvailabilityWindow), []string{agentproto.ProbeTargetTunnel, agentproto.ProbeTargetPeer}, keys).
		Find(&reports).Error; err != nil {
		return nil, err
	}
	if len(reports) == 0 {
		return nil, nil
	}
	healthy := 0
	for _, r := range reports {
		if r.Verdict == agentproto.ProbeVerdictHealthy {
			healthy++
		}
	}
	pct := float64(healthy) / float64(len(reports)) * 100
	return &pct, nil
}
