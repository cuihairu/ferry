package protocol

import (
	"fmt"
	"sync"
)

// registry is the protocol registry: each protocol file Registers during init
// and it is read-only at runtime. The RWMutex covers the concurrency surface
// (write lock at registration, read lock at runtime); a duplicate registration
// panics as a programming error -- a protocol name clash should blow up in init
// rather than be carried along while broken.
var (
	mu    sync.RWMutex
	reg   = map[string]*Protocol{}
	order []string // registration order, keeps the All() output and the endpoint / validation tests deterministic
)

// Register registers one protocol; a duplicate registration panics (a programming error at init).
func Register(p Protocol) {
	if p.Name == "" {
		panic("protocol: register without name")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := reg[p.Name]; dup {
		panic("protocol: duplicate register " + p.Name)
	}
	cp := p
	reg[p.Name] = &cp
	order = append(order, p.Name)
}

// Lookup fetches a protocol by name; an unregistered one reports "unsupported
// protocol" -- a legacy dirty-data scenario that is reported explicitly rather
// than silenced (same convention as the pre-refactor formatters' default branch).
func Lookup(name string) (*Protocol, error) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := reg[name]
	if !ok {
		return nil, fmt.Errorf("unsupported protocol %q", name)
	}
	return p, nil
}

// Has reports whether a protocol is registered (the data-driven replacement for ValidProtocol).
func Has(name string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := reg[name]
	return ok
}

// All returns every protocol (in registration order, deterministic for the endpoints and the validation tests).
func All() []*Protocol {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]*Protocol, 0, len(order))
	for _, name := range order {
		out = append(out, reg[name])
	}
	return out
}
