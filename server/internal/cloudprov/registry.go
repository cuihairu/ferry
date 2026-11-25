package cloudprov

import (
	"sync"
)

// Account is the adapter-facing credential bundle (F-0328): the decrypted
// provider credential plus its scope. The plaintext AccessKey exists only
// between the credential store and the adapter -- it is never logged and
// never returned by any API.
type Account struct {
	ProviderID uint   `json:"provider_id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	AccessKey  string `json:"-"` // plaintext, adapter-internal only
	Region     string `json:"region,omitempty"`
}

// AdapterFactory builds an Adapter for one account. Vendors register a
// factory under their provider type name at init time.
type AdapterFactory func(a Account) (Adapter, error)

var (
	regMu    sync.RWMutex
	registry = map[string]AdapterFactory{}
)

// Register wires a vendor adapter factory under a provider type name
// (e.g. "vultr", "hetzner"). Re-registering a type replaces its factory.
func Register(providerType string, f AdapterFactory) {
	regMu.Lock()
	defer regMu.Unlock()
	registry[providerType] = f
}

// Lookup returns the factory for a provider type, or nil when no adapter is
// registered (the account can then only be verified locally).
func Lookup(providerType string) AdapterFactory {
	regMu.RLock()
	defer regMu.RUnlock()
	return registry[providerType]
}

// Unregister removes a provider type's factory (adapter removal and the
// test seam).
func Unregister(providerType string) {
	regMu.Lock()
	defer regMu.Unlock()
	delete(registry, providerType)
}

// NewAdapter builds an adapter for the account; a type without a registered
// factory is an Unsupported typed error.
func NewAdapter(a Account) (Adapter, error) {
	f := Lookup(a.Type)
	if f == nil {
		return nil, NewError(ErrUnsupported, OpCapabilities, "", a.Type,
			"no adapter registered for provider type")
	}
	return f(a)
}

// RegisteredTypes lists the provider types with a registered adapter.
func RegisteredTypes() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}
