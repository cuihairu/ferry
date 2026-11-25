// Package policy resolves scoped delivery/allocation policies (F-0282~F-0296)
// and applies them: system -> plan -> user merge with per-field override
// (F-0287), node filtering with a per-node explanation (F-0296), the failover
// freeze for the allocation sweep (F-0294), and the subscription output cap
// (F-0286).
package policy

import (
	"sort"
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// CompactOutputLimit caps the delivered entry list when the effective output
// policy is "compact" (F-0286).
const CompactOutputLimit = 10

// Resolve merges every enabled policy on the chain system -> plan -> user
// (F-0287): deeper scopes override per field, absent fields inherit. Returns
// the effective constraints and the contributing rows (for the explain view).
// With no policies at all the zero value is returned -- no policies, no
// behavior change.
func Resolve(db *gorm.DB, userID, planID uint) (storage.PolicyConstraints, []storage.Policy, error) {
	var rows []storage.Policy
	if err := db.Where("enabled = ?", true).
		Where("(scope = ?) OR (scope = ? AND subject_id = ?) OR (scope = ? AND subject_id = ?)",
			storage.PolicyScopeSystem, storage.PolicyScopePlan, planID, storage.PolicyScopeUser, userID).
		Order("version ASC, id ASC").Find(&rows).Error; err != nil {
		return storage.PolicyConstraints{}, nil, err
	}
	eff := storage.PolicyConstraints{}
	for _, p := range rows {
		c := p.DecodeConstraints()
		if len(c.Regions) > 0 {
			eff.Regions = c.Regions
		}
		if len(c.ISPs) > 0 {
			eff.ISPs = c.ISPs
		}
		if len(c.LineTypes) > 0 {
			eff.LineTypes = c.LineTypes
		}
		if c.MinHealth != "" {
			eff.MinHealth = c.MinHealth
		}
		if c.MinCapacityMbps > 0 {
			eff.MinCapacityMbps = c.MinCapacityMbps
		}
		if c.MaxTrafficPriceCents > 0 {
			eff.MaxTrafficPriceCents = c.MaxTrafficPriceCents
		}
		if c.MaxMonthlyCostCents > 0 {
			eff.MaxMonthlyCostCents = c.MaxMonthlyCostCents
		}
		if c.Failover != "" {
			eff.Failover = c.Failover
		}
		if c.Output != "" {
			eff.Output = c.Output
		}
	}
	return eff, rows, nil
}

// Drop records why one node left the candidate set (the explain half of F-0296).
type Drop struct {
	NodeID   uint   `json:"node_id"`
	NodeName string `json:"node_name"`
	Reason   string `json:"reason"`
}

// ApplyConstraints filters candidate entries by the effective constraints,
// returning the kept nodes and a per-node explanation for the dropped ones.
func ApplyConstraints(nodes []storage.Node, eff storage.PolicyConstraints) ([]storage.Node, []Drop) {
	kept := make([]storage.Node, 0, len(nodes))
	dropped := []Drop{}
	for _, n := range nodes {
		if reason := nodeRejection(n, eff); reason != "" {
			dropped = append(dropped, Drop{NodeID: n.ID, NodeName: n.Name, Reason: reason})
			continue
		}
		kept = append(kept, n)
	}
	return kept, dropped
}

// nodeRejection returns the first constraint the node violates, or "" when it
// passes (evaluation order: whitelists, health, capacity, cost).
func nodeRejection(n storage.Node, eff storage.PolicyConstraints) string {
	if len(eff.Regions) > 0 && !contains(eff.Regions, n.Region) {
		return "region not allowed"
	}
	if len(eff.ISPs) > 0 && !contains(eff.ISPs, n.ISP) {
		return "isp not allowed"
	}
	if len(eff.LineTypes) > 0 && !contains(eff.LineTypes, n.LineType) {
		return "line type not allowed"
	}
	// Node health tiers (F-0291): offline -> degraded, suspended -> worse.
	// min_health=healthy keeps only fully healthy entries; degraded still
	// serves offline ones (the probe pipeline may recover them).
	if eff.MinHealth == "healthy" && (n.Status == "offline" || n.PoolState == "suspended") {
		return "below min health"
	}
	if eff.MinHealth == "degraded" && n.PoolState == "suspended" {
		return "below min health"
	}
	// Capacity (F-0292): 0 Mbps = unknown/uncapped, do not drop on it.
	if eff.MinCapacityMbps > 0 && n.BwDownMbps > 0 && n.BwDownMbps < eff.MinCapacityMbps {
		return "below min capacity"
	}
	// Cost (F-0293): only metered/fixed entries with a price are compared.
	if eff.MaxTrafficPriceCents > 0 && n.TrafficPriceCents > eff.MaxTrafficPriceCents {
		return "above traffic price cap"
	}
	if eff.MaxMonthlyCostCents > 0 && n.MonthlyCostCents > eff.MaxMonthlyCostCents {
		return "above monthly cost cap"
	}
	return ""
}

// CapOutput applies the output shaping (F-0286): compact delivers the probe-
// sorted best ten (rtt ascending, unknown rtt last); full/empty is a no-op.
func CapOutput(entries []storage.Node, rtt map[uint]int, eff storage.PolicyConstraints) []storage.Node {
	if eff.Output != "compact" || len(entries) <= CompactOutputLimit {
		return entries
	}
	sorted := make([]storage.Node, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := rtt[sorted[i].ID], rtt[sorted[j].ID]
		if ri == 0 {
			ri = 1 << 30
		}
		if rj == 0 {
			rj = 1 << 30
		}
		return ri < rj
	})
	return sorted[:CompactOutputLimit]
}

// FreezeFailover reports whether the allocation sweep is frozen (F-0294): any
// policy on the chain setting failover=manual stops auto landing switches.
func FreezeFailover(eff storage.PolicyConstraints) bool {
	return strings.EqualFold(eff.Failover, "manual")
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
