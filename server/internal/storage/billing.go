package storage

import "time"

// Cost data accounting (§4.1). Ferry keeps two clearly separated cost
// worlds: what the vendor actually charged (CostRecord/CostItem) and what
// Ferry estimates (CostEstimate). Estimates are never stored as bills and
// bills are never recomputed from instance counts times unit prices.

// CostProvider (F-0502) is one cost data source: a cloud account whose bills
// Ferry imports, or a purely manual source the operator records by hand.
type CostProvider struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// Name is the operator-facing source label.
	Name string `gorm:"size:128;not null" json:"name"`
	// ProviderID links to the cloud Provider row the bills come from; nil for
	// manual sources that have no cloud account behind them.
	ProviderID *uint `gorm:"index" json:"provider_id,omitempty"`
	// Source is the ingestion class (billing.Source* constants): cloud API,
	// uploaded file, or hand-entered records.
	Source string `gorm:"size:16;not null" json:"source"`
	// Account is the vendor-side account identifier used for attribution and
	// account-scoped bill pulls (F-0561).
	Account string `gorm:"size:128" json:"account"`
	// Region is the source's operating scope for region-scoped pulls and the
	// fallback attribution region for items the vendor leaves blank (F-0526).
	Region string `gorm:"size:64" json:"region"`
	// Currency is the bill currency (ISO 4217); amounts are in minor units.
	Currency string `gorm:"size:8;not null" json:"currency"`
	// Enabled gates whether the source is imported and aggregated.
	Enabled bool   `gorm:"default:true" json:"enabled"`
	Note    string `gorm:"size:255" json:"note"`
	// LastSyncAt / LastError track the last import run and why it failed
	// (F-0532/F-0533 live on the run log; this is the source-level rollup).
	LastSyncAt *time.Time `json:"last_sync_at,omitempty"`
	LastError  string     `gorm:"size:255" json:"last_error,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// CostRecord (F-0503) is one raw bill record exactly as the vendor issued it.
// Revisions of the same bill share the DedupeKey and supersede the previous
// row instead of duplicating it (F-0512/F-0530).
type CostRecord struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// CostProviderID points at the source the record came from (index, F-0513).
	CostProviderID uint `gorm:"index;not null" json:"cost_provider_id"`
	// Source carries the ingestion class for filtering (F-0533).
	Source string `gorm:"size:16;not null" json:"source"`
	// BillID is the provider's original bill id (F-0511); Revision marks a
	// reissued bill ("" for the original, "1","2",… for revisions).
	BillID   string `gorm:"size:128;not null" json:"bill_id"`
	Revision string `gorm:"size:16;not null;default:''" json:"revision"`
	Account  string `gorm:"size:128" json:"account"`
	Region   string `gorm:"size:64" json:"region"`
	// Currency is the bill currency; AmountCents is the total in minor units.
	Currency string `gorm:"size:8;not null" json:"currency"`
	// AmountCents is the total charged (pre-tax + tax) in currency minor units.
	AmountCents int64 `json:"amount_cents"`
	// PreTaxCents / TaxCents carry the tax split when the provider supplies it
	// (F-0510); zero means the split was not provided.
	PreTaxCents int64 `json:"pre_tax_cents"`
	TaxCents    int64 `json:"tax_cents"`
	// Timing (F-0509): IncurredAt is when the cost was incurred, PeriodStart/
	// PeriodEnd are the billing cycle, ImportedAt is when Ferry stored it.
	IncurredAt  time.Time `gorm:"index" json:"incurred_at"`
	PeriodStart time.Time `gorm:"index" json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	ImportedAt  time.Time `gorm:"index" json:"imported_at"`
	ImportedBy  string    `gorm:"size:128" json:"imported_by"`
	// RawSnapshot is the vendor's original field payload for traceability
	// (F-0511); it is never interpreted by Ferry.
	RawSnapshot string `gorm:"type:text" json:"raw_snapshot,omitempty"`
	// FX (F-0508): Rate is the units-to-currency exchange rate expressed as a
	// float (0 = unknown), RateDate is the day the rate was observed, RateSource
	// names the feed (manual/ECB/...). ReportAmountCents is the converted total
	// when Rate>0.
	FXRate            float64    `json:"fx_rate,omitempty"`
	FXRateDate        *time.Time `json:"fx_rate_date,omitempty"`
	FXRateSource      string     `gorm:"size:32" json:"fx_rate_source,omitempty"`
	ReportAmountCents int64      `json:"report_amount_cents,omitempty"`
	// Status (F-0597): unsettled (still accruing) or settled (closed bill).
	Status string `gorm:"size:16;not null;default:unsettled" json:"status"`
	// DedupeKey is the upsert key (F-0512): one physical index, one record.
	DedupeKey string    `gorm:"size:255;not null;uniqueIndex:idx_cost_record_dedupe" json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CostItem (F-0504) is one line item of a CostRecord: the finest charge the
// vendor split the bill into, with the resource/region attribution the
// allocation engine consumes (F-0561~F-0565).
type CostItem struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// CostRecordID is the parent bill record (index).
	CostRecordID uint `gorm:"index;not null" json:"cost_record_id"`
	// ResourceID is the provider-side resource identifier; empty for account
	// level charges (e.g. bandwidth pools).
	ResourceID string `gorm:"size:128;index" json:"resource_id"`
	// NodeID links the charge to a bound Ferry Node when known (F-0562); nil
	// until the resource is bound or matched.
	NodeID *uint `gorm:"index" json:"node_id,omitempty"`
	// PathID links the charge to a Path when it can be matched to one (F-0561).
	PathID *uint  `json:"path_id,omitempty"`
	Region string `gorm:"size:64;index" json:"region"`
	// Category is the charge kind (billing.Category* constants) used for
	// filtering and estimate comparison.
	Category string `gorm:"size:32;not null;default:other" json:"category"`
	// AmountCents is the item total in the record currency minor units.
	AmountCents int64 `json:"amount_cents"`
	// Usage carries the consumed quantity when the vendor reports one
	// (GB/hours/Mbps...); zero when the charge is a flat fee.
	Usage int64  `gorm:"default:0" json:"usage"`
	Unit  string `gorm:"size:16" json:"unit"`
	// Description is the vendor's line-item label, kept verbatim.
	Description string `gorm:"size:255" json:"description"`
	// DedupeKey is the upsert key inside one record (F-0512/F-0570): an
	// allocation against the same (item, rule version, target) collides and is
	// treated as already processed.
	DedupeKey string    `gorm:"size:255;not null;uniqueIndex:idx_cost_item_dedupe" json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CostEstimate (F-0506) is one Ferry-internal estimate record, stored strictly
// separate from bills (F-0552). An estimate never pretends to be settled and
// is never overwritten by a bill.
type CostEstimate struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// NodeID / PathID / Region carry the estimate scope (F-0561).
	NodeID *uint  `gorm:"index" json:"node_id,omitempty"`
	PathID *uint  `json:"path_id,omitempty"`
	Region string `gorm:"size:64" json:"region"`
	// Kind is the estimate class (billing.Kind* constants).
	Kind string `gorm:"size:32;not null" json:"kind"`
	// AmountCents is the estimated cost in EstimateCurrency minor units.
	AmountCents int64  `json:"amount_cents"`
	Currency    string `gorm:"size:8;not null" json:"currency"`
	// PriceSource / PriceAt (F-0550): where the unit price came from and the
	// day it took effect; a missing price is flagged, never fabricated (F-0551).
	PriceSource string     `gorm:"size:128" json:"price_source"`
	PriceAt     *time.Time `json:"price_at,omitempty"`
	// UnknownPrice marks a line where no price was available (F-0551): the
	// amount is zero and the UI must show "unknown" instead of a number.
	UnknownPrice bool `gorm:"default:false" json:"unknown_price"`
	// DedupeKey is the upsert key per scope/kind/period.
	DedupeKey   string    `gorm:"size:255;not null;uniqueIndex:idx_cost_estimate_dedupe" json:"-"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CostAllocation (F-0505) is one allocation result: how much of an item's
// original cost landed on one attribution target under one rule version. The
// original cost is always kept alongside the split result (F-0569), and the
// unique (item, rule version, target) key blocks double allocation (F-0570).
type CostAllocation struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// CostItemID is the source bill item (index).
	CostItemID uint `gorm:"index;not null" json:"cost_item_id"`
	// RuleID / RuleVersion identify the rule snapshot that produced the row
	// (F-0568): recomputation creates a new version instead of rewriting
	// history (F-0571).
	RuleID      uint `gorm:"index" json:"rule_id"`
	RuleVersion int  `json:"rule_version"`
	// TargetKind is the attribution object (billing.Target* constants:
	// provider/account/resource/node/region/path).
	TargetKind string `gorm:"size:16;not null" json:"target_kind"`
	// TargetID is the row id of the target when it has one (node/path/...).
	TargetID uint `json:"target_id"`
	// TargetKey is a free-form key when the target has no row id (account,
	// region).
	TargetKey string `gorm:"size:128" json:"target_key"`
	// Basis is the split method (billing.Basis* constants: direct/bandwidth/
	// traffic/ratio/manual) and RatioPPM the fixed-ratio numerator in ppm.
	Basis    string `gorm:"size:16;not null" json:"basis"`
	RatioPPM int64  `json:"ratio_ppm"`
	// OriginalCents / AllocatedCents keep both sides of the split (F-0569).
	OriginalCents  int64 `json:"original_cents"`
	AllocatedCents int64 `json:"allocated_cents"`
	// DedupeKey blocks allocating the same item twice under one rule version
	// (F-0570); recomputation under a new version gets its own key space.
	DedupeKey string    `gorm:"size:255;not null;uniqueIndex:idx_cost_alloc_dedupe" json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CostReconciliation (F-0507) is one estimate-vs-actual comparison entry: the
// gap between what Ferry estimated and what the vendor billed for a scope and
// period, with the operator confirmation state (F-0591/F-0592).
type CostReconciliation struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// ScopeKind / TargetID / TargetKey carry the comparison bucket (F-0561).
	ScopeKind string `gorm:"size:16;not null" json:"scope_kind"`
	TargetID  uint   `gorm:"index" json:"target_id"`
	TargetKey string `gorm:"size:128" json:"target_key"`
	// PeriodStart / PeriodEnd bound the compared billing cycle.
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	Currency    string    `gorm:"size:8;not null" json:"currency"`
	// EstimateCents / ActualCents / DiffCents: Ferry estimate, vendor bill and
	// the difference (actual - estimate) in minor units.
	EstimateCents int64 `json:"estimate_cents"`
	ActualCents   int64 `json:"actual_cents"`
	DiffCents     int64 `json:"diff_cents"`
	// Status is open/confirmed/adjusted (F-0591); Note and ApprovedBy carry
	// the operator's confirmation (F-0592).
	Status     string    `gorm:"size:16;not null;default:open" json:"status"`
	Note       string    `gorm:"size:255" json:"note"`
	ApprovedBy string    `gorm:"size:128" json:"approved_by"`
	DedupeKey  string    `gorm:"size:255;not null;uniqueIndex:idx_cost_recon_dedupe" json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// BillImport (F-0521~F-0534) is one import run: which source, which account and
// region window, what was fetched, how many rows were added/updated/skipped,
// and why it failed. Repeated imports are deduped by CostRecord keys; a failed
// run keeps the retry state visible (F-0532) and the raw payload reference for
// traceability (F-0531).
type BillImport struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// CostProviderID is the source being imported (index).
	CostProviderID uint `gorm:"index;not null" json:"cost_provider_id"`
	// Source / Trigger: api/file/manual and manual/scheduled/backfill.
	Source  string `gorm:"size:16;not null" json:"source"`
	Trigger string `gorm:"size:16;not null" json:"trigger"`
	// PeriodStart / PeriodEnd are the requested billing window (F-0525);
	// Account / Region narrow the request (F-0526).
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	Account     string    `gorm:"size:128" json:"account"`
	Region      string    `gorm:"size:64" json:"region"`
	// State is running/succeeded/failed/partial; Error carries the failure
	// reason (F-0532) and RetryCount the retry attempt number.
	State      string `gorm:"size:16;not null" json:"state"`
	Fetched    int    `json:"fetched"`
	Added      int    `json:"added"`
	Updated    int    `json:"updated"`
	Skipped    int    `json:"skipped"`
	Error      string `gorm:"size:512" json:"error,omitempty"`
	RetryCount int    `json:"retry_count"`
	// RawRef points at the retained raw payload/file (F-0531, F-0534).
	RawRef     string     `gorm:"size:255" json:"raw_ref,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// CostAllocationRule (F-0563~F-0568) is one versioned allocation rule. Rows are
// append-only per version: editing a rule creates a new version so old
// allocation results stay explainable (F-0568/F-0571).
type CostAllocationRule struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// Name / Kind: the rule label and split basis (bandwidth/traffic/ratio/
	// manual/direct).
	Name string `gorm:"size:128;not null" json:"name"`
	Kind string `gorm:"size:16;not null" json:"kind"`
	// Version increments on every edit; Enabled retires a rule without
	// deleting history.
	Version int  `gorm:"not null;default:1" json:"version"`
	Enabled bool `gorm:"default:true" json:"enabled"`
	// Params is the rule payload (fixed ratios, target list, category filter).
	Params    string    `gorm:"type:text" json:"params,omitempty"`
	Note      string    `gorm:"size:255" json:"note"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NodeCostLine (F-0542, F-0545~F-0549) is one manual recurring cost line of a
// node beyond its base billing: hourly compute, extra public IP, disk,
// snapshot, backup, discount/reserved-instance input, or a free-form custom
// item. Hand-entered prices are the sole authority (design 套餐与成本 §2); the
// estimate engine turns these lines into CostEstimate rows.
type NodeCostLine struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// NodeID is the billed node (index).
	NodeID uint `gorm:"index;not null" json:"node_id"`
	// Kind is the cost class (billing.Kind* constants minus instance/traffic,
	// which are computed from the node's base billing fields).
	Kind string `gorm:"size:16;not null" json:"kind"`
	// HourlyPriceCents is the hourly compute price (F-0542); the engine
	// multiplies it by the hours of the estimate period. Only for kind=hourly.
	HourlyPriceCents int64 `gorm:"default:0" json:"hourly_price_cents"`
	// AmountCents is the fixed monthly amount in minor units; negative only
	// for kind=discount (F-0548 discount/reserved-instance input).
	AmountCents int64 `json:"amount_cents"`
	// UnknownPrice marks a line whose price the operator does not know yet
	// (F-0551): the amount is treated as zero and the UI shows "unknown".
	UnknownPrice bool   `gorm:"default:false" json:"unknown_price"`
	Currency     string `gorm:"size:8;not null" json:"currency"`
	// Note is the operator's label (e.g. "reserved instance 1y", "extra IP x2").
	Note      string    `gorm:"size:255" json:"note"`
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
