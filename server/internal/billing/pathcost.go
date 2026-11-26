package billing

import (
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// PriceFreshWindow bounds how long a node's price columns are trusted as fresh
// (F-0604): the price columns carry no dedicated timestamp, so the node row's
// UpdatedAt is the price as-of marker (only panel edits and registration meta
// sync touch them). An UpdatedAt older than the window makes the price stale --
// never silently treated as exact (F-0610).
const PriceFreshWindow = 7 * 24 * time.Hour

// LegCost is the explainable cost line of one Path leg (F-0603): what the leg
// costs, where the price came from, and whether it is known/fresh.
type LegCost struct {
	Role        string `json:"role"` // entry/landing/relay
	NodeID      uint   `json:"node_id"`
	Name        string `json:"name"`
	BillingType string `json:"billing_type"`
	Unit        string `json:"unit"` // per_gb / fixed
	// CentsPerGB is the marginal traffic price (metered legs only, 0 otherwise);
	// MonthlyCents is the fixed monthly price (fixed legs, sunk cost, excluded
	// from the marginal sum by design -- alloc doc §C.5.2).
	CentsPerGB   int64      `json:"cents_per_gb"`
	MonthlyCents int64      `json:"monthly_cents"`
	Known        bool       `json:"known"` // price exists (metered >0 / fixed >0)
	Fresh        bool       `json:"fresh"` // price as-of within PriceFreshWindow
	AsAt         *time.Time `json:"as_at,omitempty"`
	Source       string     `json:"source"`
	// Unknown marks a leg whose period amount cannot be computed (price unknown
	// or stale); it contributes nothing to the total and the estimate loses its
	// Precise flag instead of inventing a number (F-0610).
	Unknown bool `json:"unknown"`
}

// PathCost is the explainable cost estimate of one Path over a period (F-0603).
type PathCost struct {
	PathID     uint      `json:"path_id"`
	Name       string    `json:"name"`
	Direction  string    `json:"direction"`
	Currency   string    `json:"currency"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Explain    []LegCost `json:"explain"`
	TotalCents int64     `json:"total_cents"` // sum of the legs that could be computed
	// Precise is true only when every leg's amount could be computed from a
	// known and fresh price; false results must not be presented as exact
	// (F-0610).
	Precise bool `json:"precise"`
	// MarginalPerGBCents sums the metered legs' unit prices (the number the
	// scheduler can reason about per extra GB); fixed legs are sunk cost.
	MarginalPerGBCents int64 `json:"marginal_per_gb_cents"`
}

// legCostOf derives one leg's explainable cost line from the node row.
func legCostOf(role string, n storage.Node, now time.Time) LegCost {
	leg := LegCost{Role: role, NodeID: n.ID, Name: n.Name, BillingType: n.BillingType}
	fresh := n.UpdatedAt.After(now.Add(-PriceFreshWindow)) || n.UpdatedAt.Equal(now.Add(-PriceFreshWindow))
	leg.Fresh = fresh
	if !n.UpdatedAt.IsZero() {
		at := n.UpdatedAt
		leg.AsAt = &at
	}
	switch n.BillingType {
	case KindTraffic, "按流量":
		leg.Unit = "per_gb"
		leg.CentsPerGB = n.TrafficPriceCents
		leg.Source = priceNodeTraffic
		leg.Known = n.TrafficPriceCents > 0
	default:
		leg.Unit = "fixed"
		leg.MonthlyCents = n.MonthlyCostCents
		leg.Source = priceNodeMonthly
		leg.Known = n.MonthlyCostCents > 0
	}
	if !leg.Known || !leg.Fresh {
		leg.Unknown = true
	}
	return leg
}

// legAmount computes one leg's period amount: metered legs from the node's
// recorded traffic x unit price (reuse the estimate engine's traffic query),
// fixed legs prorated over the month. The second return is false when the
// amount cannot be computed (unknown or stale price, F-0610).
func legAmount(db *gorm.DB, leg LegCost, nodeID uint, from, to, now time.Time) (int64, bool) {
	if leg.Unknown {
		return 0, false
	}
	if leg.Unit == "per_gb" {
		var n storage.Node
		n.ID = nodeID
		n.TrafficPriceCents = leg.CentsPerGB
		cents, ok := periodTrafficCents(db, &n, from, to)
		return cents, ok
	}
	return int64(float64(leg.MonthlyCents) * periodFractionOfMonth(from, to)), true
}

// PathCostEstimate builds the explainable cost estimate for one Path
// (F-0603): every leg's line carries its price, source, freshness and
// unknown/stale marking; TotalCents sums only computable legs and Precise
// stays false unless every leg could be computed (F-0610).
func PathCostEstimate(db *gorm.DB, path storage.Path, from, to, now time.Time) (PathCost, error) {
	pc := PathCost{
		PathID: path.ID, Name: path.Name, Direction: path.Direction,
		From: from, To: to, Explain: []LegCost{},
	}
	legs := []struct {
		role string
		id   uint
	}{
		{"entry", path.EntryNodeID},
		{"landing", path.LandingNodeID},
	}
	if path.RelayNodeID != nil {
		legs = append(legs, struct {
			role string
			id   uint
		}{"relay", *path.RelayNodeID})
	}
	pc.Precise = true
	for _, spec := range legs {
		var n storage.Node
		if err := db.First(&n, spec.id).Error; err != nil {
			return pc, err
		}
		leg := legCostOf(spec.role, n, now)
		if pc.Currency == "" {
			pc.Currency = n.Currency
		}
		if amount, ok := legAmount(db, leg, n.ID, from, to, now); ok {
			leg.Unknown = false
			pc.TotalCents += amount
		} else {
			leg.Unknown = true
			pc.Precise = false
		}
		if leg.Unit == "per_gb" && leg.Known {
			pc.MarginalPerGBCents += leg.CentsPerGB
		}
		pc.Explain = append(pc.Explain, leg)
	}
	return pc, nil
}

// PathCostEstimates computes the estimate for every enabled Path (F-0603),
// skipping missing nodes instead of failing the whole listing.
func PathCostEstimates(db *gorm.DB, from, to, now time.Time) ([]PathCost, error) {
	var paths []storage.Path
	if err := db.Where("enabled = ?", true).Find(&paths).Error; err != nil {
		return nil, err
	}
	out := make([]PathCost, 0, len(paths))
	for _, p := range paths {
		pc, err := PathCostEstimate(db, p, from, to, now)
		if err != nil {
			continue // a path referencing a deleted node just yields no line
		}
		out = append(out, pc)
	}
	return out, nil
}
