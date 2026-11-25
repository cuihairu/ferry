// Package cloudprov defines the unified Cloud Provider Adapter contract
// (F-0302~F-0318): one Go interface every cloud vendor adapter implements, so
// Dash can manage cloud connections, list/inspect resources, run supported
// power operations, and import billing without vendor-specific code paths.
//
// The interface is capability-driven (F-0303): an adapter declares up front
// which operations it supports (power control, force stop, rebuild, billing
// import) and the API version it speaks (F-0317); callers must check
// Capabilities before invoking a gated operation, and invoking an unsupported
// one still fails closed with an Unsupported *Error (F-0315). Client wraps
// any Adapter with the shared timeout/rate-limit/retry policy (F-0316).
// ContractSuite (contract_test.go) is the executable contract every adapter
// must pass (F-0318); the reference Fake implementation lives in fake.go and
// doubles as the test double for later handler-layer work.
//
// This package holds the contract only: vendor SDKs, credential storage
// (Provider rows stay in storage), resource persistence (F-0341+), and the
// HTTP surface are wired in later regions.
package cloudprov

import (
	"context"
	"time"
)

// Resource lifecycle states carried by Resource.State / RefreshStatus
// (F-0306). The set is deliberately small: vendor states map onto these in
// the adapter, not in the callers.
const (
	StateRunning      = "running"
	StateStopped      = "stopped"
	StateStarting     = "starting"
	StateStopping     = "stopping"
	StateRebuilding   = "rebuilding"
	StateProvisioning = "provisioning"
	StateError        = "error"
)

// ValidState reports whether s is a canonical resource state.
func ValidState(s string) bool {
	switch s {
	case StateRunning, StateStopped, StateStarting, StateStopping,
		StateRebuilding, StateProvisioning, StateError:
		return true
	}
	return false
}

// HighRiskForceStop names the force-stop operation for permission wiring
// (F-0310): invoking it must require an elevated panel permission (or an
// explicit confirmation) at the handler layer; the adapter itself has no
// session context to check against.
const HighRiskForceStop = "force_stop"

// Capabilities is an adapter's self-declared feature set (F-0303) plus its
// API compatibility info (F-0317). Every gated Adapter operation must be
// reflected here: a false flag means the caller should hide the action, and
// the adapter rejects it with ErrUnsupported anyway (fail closed).
type Capabilities struct {
	// APIVersion is the vendor API version this adapter targets (dotted
	// numeric, e.g. "2.1"); compared with VersionAtLeast (F-0317).
	APIVersion string `json:"api_version"`
	// Power gates PowerOn/PowerOff/Reboot (F-0307~F-0309).
	Power bool `json:"power"`
	// ForceStop gates the high-risk force stop (F-0310); implies Power.
	ForceStop bool `json:"force_stop"`
	// Rebuild gates Rebuild (F-0311): only declared-rebuild-capable vendors
	// expose reinstall/recreate in the panel.
	Rebuild bool `json:"rebuild"`
	// Billing gates ImportBilling (F-0314).
	Billing bool `json:"billing"`
	// Regions lists the account-visible region identifiers (may be empty
	// when the vendor has no region concept).
	Regions []string `json:"regions,omitempty"`
}

// Filter narrows a ListResources call (F-0304): empty fields mean no
// restriction.
type Filter struct {
	Region string            `json:"region,omitempty"`
	Tags   map[string]string `json:"tags,omitempty"`
}

// Resource is the unified resource view (F-0304/F-0305). PublicIP carries
// the public address (F-0312, empty when none/unassigned); BwMbps and Spec
// carry the bandwidth/spec facts (F-0313, zero/empty when unknown).
type Resource struct {
	// ResourceID is the provider-side identifier (stable across refreshes).
	ResourceID string `json:"resource_id"`
	Name       string `json:"name"`
	Region     string `json:"region"`
	State      string `json:"state"` // one of the State* constants
	// PublicIP is the public IPv4 address (F-0312).
	PublicIP string `json:"public_ip,omitempty"`
	// BwMbps is the billed bandwidth in Mbps (F-0313; 0 = unknown).
	BwMbps int `json:"bw_mbps,omitempty"`
	// Spec is the plan/slug identifier (F-0313).
	Spec string            `json:"spec,omitempty"`
	Tags map[string]string `json:"tags,omitempty"`
}

// BillingWindow selects the billing import range (F-0314); From/To are
// inclusive calendar bounds, zero To = "up to now".
type BillingWindow struct {
	From time.Time
	To   time.Time
}

// BillingRecord is one imported cost datum (F-0314), keyed to the
// provider-side resource so it can reconcile against the cost ledger.
type BillingRecord struct {
	ResourceID string    `json:"resource_id"`
	CostCents  int64     `json:"cost_cents"`
	Currency   string    `json:"currency"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
}

// Adapter is the unified cloud provider contract (F-0302). All methods take
// a context and must honor its cancellation; all failures come back as
// *Error (F-0315) so callers can branch on Type without vendor knowledge.
// Implementations must be safe for concurrent use.
type Adapter interface {
	// Capabilities reports the vendor's feature flags and API version
	// (F-0303/F-0317).
	Capabilities(ctx context.Context) (Capabilities, error)
	// ListResources queries the account's resources (F-0304), narrowed by f.
	ListResources(ctx context.Context, f Filter) ([]Resource, error)
	// DescribeResource returns one resource's full view (F-0305); a missing
	// id is a NotFound error, not nil.
	DescribeResource(ctx context.Context, resourceID string) (*Resource, error)
	// RefreshStatus re-reads just the lifecycle state (F-0306).
	RefreshStatus(ctx context.Context, resourceID string) (string, error)
	// PowerOn boots a stopped resource (F-0307).
	PowerOn(ctx context.Context, resourceID string) error
	// PowerOff performs a graceful shutdown (F-0308).
	PowerOff(ctx context.Context, resourceID string) error
	// Reboot performs a graceful restart (F-0309).
	Reboot(ctx context.Context, resourceID string) error
	// ForceStop pulls the virtual power plug (F-0310). HIGH RISK: callers
	// must gate this behind an elevated permission (HighRiskForceStop) and
	// surface a confirmation; the adapter enforces only the capability flag.
	ForceStop(ctx context.Context, resourceID string) error
	// Rebuild reinstalls/recreates the resource from an image (F-0311).
	// Vendors that cannot rebuild must declare Rebuild=false; the destroy +
	// recreate semantics (data loss) belong to the vendor call.
	Rebuild(ctx context.Context, resourceID, image string) error
	// ImportBilling pulls cost data for the window (F-0314).
	ImportBilling(ctx context.Context, w BillingWindow) ([]BillingRecord, error)
}
