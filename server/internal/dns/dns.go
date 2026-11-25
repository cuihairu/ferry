// Package dns is the DNS provider API plugin slot (BR-2 domain fronting: the domain does not change,
// the IP does). The interface is minimized to upserting one record: A-record switching is driven by recovery tier
// L1, while TXT records are reserved for BR-4 ACME DNS-01 to reuse the same credentials and channel.
// Unknown kinds never claim support (same policy as the supply providers): entry is validated via KnownKind and
// execution via New, which errors out; adding a vendor means adding one case in New.
package dns

import (
	"context"
	"errors"
	"fmt"
)

// Provider is the minimal write channel for one DNS vendor.
type Provider interface {
	// Kind is the plugin-slot type name (corresponding to the recorded Type).
	Kind() string
	// Upsert idempotently writes name's rtype record to value (change if present, create if absent).
	Upsert(ctx context.Context, name, rtype, value string) error
	// Delete removes all rtype records for name (no records counts as success) - used for
	// record withdrawal when a whole region is down (E-27); all records of the same name are removed to keep the withdrawal clean.
	Delete(ctx context.Context, name, rtype string) error
}

// kinds are the wired-in plugin-slot kinds (shared by KnownKind/New; register a new vendor here).
var kinds = []string{"cloudflare"}

// KnownKind validates whether a kind is wired in (guards the entry layer against false claims).
func KnownKind(kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// New builds the write channel by kind; token is the decrypted plaintext credential and does not cross the method boundary.
func New(kind, token string) (Provider, error) {
	if token == "" {
		return nil, errors.New("dns: provider token not configured")
	}
	switch kind {
	case "cloudflare":
		return &Cloudflare{Token: token, Base: defaultBase}, nil
	default:
		return nil, fmt.Errorf("dns: provider type %q not supported yet (%v)", kind, kinds)
	}
}
