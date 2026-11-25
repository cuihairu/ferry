package storage

import (
	"encoding/json"
	"fmt"
	"time"
)

// Policy scopes (F-0283~F-0285): one policy row per scope+subject; resolution
// merges system -> plan -> user with the deeper scope winning per field.
const (
	PolicyScopeSystem = "system"
	PolicyScopePlan   = "plan"
	PolicyScopeUser   = "user"
)

// ValidPolicyScope reports whether s is a legal policy scope.
func ValidPolicyScope(s string) bool {
	return s == PolicyScopeSystem || s == PolicyScopePlan || s == PolicyScopeUser
}

// Policy is one scope's delivery/allocation constraints (F-0282): a JSON
// constraint payload on top of scope targeting. Constraints is always the
// FULL field set for that scope (sparse fields are simply absent = inherit).
// Version bumps on every update and UpdatedBy carries the last editor, so
// change history is readable from the row itself plus the audit trail
// (auditMiddleware snapshots every write) -- F-0295.
type Policy struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Scope       string    `gorm:"size:8;not null;uniqueIndex:idx_policy_scope,priority:1" json:"scope"`         // system/plan/user
	SubjectID   uint      `gorm:"not null;default:0;uniqueIndex:idx_policy_scope,priority:2" json:"subject_id"` // plan id / user id; 0 = whole scope
	Constraints string    `gorm:"type:text" json:"constraints"`                                                 // JSON PolicyConstraints
	Enabled     bool      `gorm:"default:true" json:"enabled"`
	Version     int       `json:"version"`                   // bumped on every update (F-0295)
	UpdatedBy   string    `gorm:"size:64" json:"updated_by"` // last editor (admin username)
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PolicyConstraints is the constraint payload carried in Policy.Constraints
// (F-0286~F-0294). Every field is optional; an absent field inherits from the
// enclosing scope during resolution.
type PolicyConstraints struct {
	// Region/ISP/line-type whitelists (F-0288/F-0289/F-0290): empty = no
	// restriction, non-empty = the entry's dimension must be listed.
	Regions   []string `json:"regions,omitempty"`
	ISPs      []string `json:"isps,omitempty"`
	LineTypes []string `json:"line_types,omitempty"`
	// MinHealth (F-0291): the minimum node health tier served --
	// "healthy" drops offline/suspended entries, "degraded" only drops
	// suspended ones; empty = no gate.
	MinHealth string `json:"min_health,omitempty"`
	// MinCapacityMbps (F-0292): entries below the plan bandwidth are dropped.
	MinCapacityMbps int `json:"min_capacity_mbps,omitempty"`
	// Cost caps (F-0293): metered entries above the traffic unit price and
	// fixed entries above the monthly cost are dropped (cents).
	MaxTrafficPriceCents int64 `json:"max_traffic_price_cents,omitempty"`
	MaxMonthlyCostCents  int64 `json:"max_monthly_cost_cents,omitempty"`
	// Failover (F-0294): "manual" freezes the auto landing allocation sweep
	// (current assignments are kept); empty/auto keeps sweeping.
	Failover string `json:"failover,omitempty"`
	// Output (F-0286): subscription output shaping -- "compact" caps the
	// delivered entry list to the probe-sorted best ten; empty/full delivers all.
	Output string `json:"output,omitempty"`
	// PathGroupID (F-0270): pins subscription delivery to one path group --
	// the group resolves to its serving path (default first, then candidates
	// by position, health-aware) and only that path's entry is delivered.
	// 0 = unset/inherit.
	PathGroupID uint `json:"path_group_id,omitempty"`
}

// ParsePolicyConstraints decodes and validates a constraint payload; unknown
// health/failover/output values are rejected outright (strict mode, same
// spirit as the self-built subscription filter).
func ParsePolicyConstraints(raw string) (PolicyConstraints, error) {
	var out PolicyConstraints
	if raw == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, err
	}
	switch out.MinHealth {
	case "", "healthy", "degraded":
	default:
		return out, fmt.Errorf("min_health must be healthy/degraded")
	}
	switch out.Failover {
	case "", "auto", "manual":
	default:
		return out, fmt.Errorf("failover must be auto/manual")
	}
	switch out.Output {
	case "", "full", "compact":
	default:
		return out, fmt.Errorf("output must be full/compact")
	}
	if out.MinCapacityMbps < 0 || out.MaxTrafficPriceCents < 0 || out.MaxMonthlyCostCents < 0 {
		return out, fmt.Errorf("numeric constraints must be >= 0")
	}
	return out, nil
}

// MustPolicyConstraints marshals constraints for storage; the input is always
// valid (it comes from ParsePolicyConstraints or code defaults).
func MustPolicyConstraints(c PolicyConstraints) string {
	raw, err := json.Marshal(c)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// DecodeConstraints decodes the row's payload; a corrupt value decodes empty
// (inherit-everything) rather than failing delivery.
func (p Policy) DecodeConstraints() PolicyConstraints {
	c, _ := ParsePolicyConstraints(p.Constraints)
	return c
}
