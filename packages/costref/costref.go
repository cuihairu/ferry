// Package costref is the cost-reference library plugin slot ("Plans and Cost Design" §4):
// the shared contract for reference-price sources. Three points of the spec —
//
//  1. Manual cost is the sole authority: the unit price / monthly fixed cost on
//     nodes is the data source for allocation and dashboards, a reference price
//     only hints and never rewrites the manual price;
//  2. Scaling out sources = implementing one Source: infracost-style open-source
//     price libraries target terraform resources on AWS/GCP/Azure and have no
//     VPS / reseller vocabulary, so the first form does not wire in a library and
//     instead uses a data-driven public price table (StaticSource, imported and
//     maintained by the panel); before wiring a library in later, verify the
//     license first (open-source-first directive);
//  3. Compare produces deviation hints (manual price off from the reference),
//     never actions — manual review decides.
//
// This package depends only on the standard library, shared by the server and future tooling.
package costref

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is the reference price not being found in the source (callers treat it as a miss, not a bad account).
var ErrNotFound = errors.New("costref: no reference price for query")

// Source is the plugin slot for one reference-price source (design §4.1).
type Source interface {
	// Name is the source name (infracost / pricelist / manual_watch …),
	// recorded alongside the Quote and in logs.
	Name() string
	// Lookup fetches the reference price by machine/region/spec (monthly price and traffic unit price).
	// Returns ErrNotFound when nothing is found.
	Lookup(ctx context.Context, q Query) (Quote, error)
}

// Query is the locator key for one reference-price lookup.
type Query struct {
	Provider string `json:"provider"` // cloud vendor / merchant
	Region   string `json:"region"`
	Spec     string `json:"spec"` // machine spec (CPU/memory/bandwidth/traffic), vocabulary managed within the source
}

// Quote is one reference price.
type Quote struct {
	MonthlyCents int64     `json:"monthly_cents"` // monthly reference price (cents/month)
	TrafficPrice int64     `json:"traffic_price"` // traffic unit price (cents/GB), 0 when absent
	Currency     string    `json:"currency"`      // currency of the reference price
	URL          string    `json:"url,omitempty"` // pricing page, clickable for verification
	At           time.Time `json:"at"`            // snapshot time
}
