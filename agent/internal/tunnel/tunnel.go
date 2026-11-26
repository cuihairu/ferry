// Package tunnel is the transport plugin abstraction (E-6): it decouples tunnel establishment from transport implementations.
//
// P0 rule: a single stream plus application-layer ping/pong heartbeats; multiplexing (several streams on one connection) is a P1 extension slot,
// and the interface reserves no OpenStream for now -- when needed, a plugin can encapsulate it itself over the connection Dial returns.
package tunnel

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// Tunnel is the transport plugin interface: establish an outbound tunnel connection and run application heartbeats on it.
type Tunnel interface {
	// Name is the plugin name (the registry key).
	Name() string
	// Dial establishes one outbound connection (the plugin owns traffic camouflage); a cancelled ctx gives up.
	Dial(ctx context.Context, addr string) (net.Conn, error)
	// ServeHeartbeat runs a ping/pong heartbeat on an established connection until the ctx is cancelled or
	// the connection drops; a nil return means the ctx ended normally, and a non-nil one leaves reconnection to the caller.
	ServeHeartbeat(ctx context.Context, conn net.Conn, interval time.Duration) error
	// Close releases the plugin's global resources (listeners etc.; a pure dialer may implement it as a no-op).
	Close() error
}

// Factory builds a plugin instance from options.
type Factory func(Options) (Tunnel, error)

var (
	mu       sync.Mutex
	registry = map[string]Factory{}
)

// Register registers a transport plugin (registering twice returns an error, surfacing name conflicts early).
func Register(name string, f Factory) error {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[name]; dup {
		return fmt.Errorf("tunnel plugin %q already registered", name)
	}
	registry[name] = f
	return nil
}

// Open builds a plugin by name; an unknown name errors (the caller falls back to the default plugin or reports, never silently connecting bare).
func Open(name string, opts Options) (Tunnel, error) {
	mu.Lock()
	f, ok := registry[name]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown tunnel plugin %q", name)
	}
	return f(opts)
}

// Names returns the registered plugin names (no ordering guarantee; for display).
func Names() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	return out
}

// Options are the common plugin options: the camouflage target and timeouts.
type Options struct {
	// ServerName is the TLS SNI/camouflage domain (empty means connecting straight to the address itself, no camouflage).
	// ssh plugin semantics: an sshd address (host:port).
	ServerName string
	// Timeout is the per-Dial timeout; 0 takes DefaultDialTimeout.
	Timeout time.Duration
	// CAFile is the private CA certificate path (empty uses the system root certificates; mTLS deployments and self-built CAs go here).
	CAFile string

	// the fields below are used only by the ssh plugin (E-28); other plugins ignore them.

	// AuthUser is the SSH login user (publickey is the only authentication form; there is no password login surface).
	AuthUser string
	// KeyFile is the private key file path (a local agent path reference; the key content goes neither into config nor into the database).
	KeyFile string
	// KnownHostsFile is an OpenSSH-format known_hosts path (host key verification);
	// empty = skip verification, allowed only during bootstrap; production must configure it.
	KnownHostsFile string
	// ForwardAddr is the direct-tcpip target: the local ferry relay listen address on the landing host
	// (e.g. 127.0.0.1:10800); sshd dials it locally after receiving the channel.
	ForwardAddr string
}

// DefaultDialTimeout is the default Dial timeout.
const DefaultDialTimeout = 10 * time.Second

// TimeoutOrDefault normalizes the timeout.
func (o Options) TimeoutOrDefault() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return DefaultDialTimeout
}

// Caps is a transport plugin's feature declaration (F-0822): the relay core and
// the panel read the struct instead of special-casing plugin names. A zero
// field means the feature is absent -- declarations are additive, so new
// plugins only state what they actually do.
type Caps struct {
	// Camouflage means outbound traffic presents as ordinary TLS/HTTPS to the
	// Options.ServerName camouflage domain (browser-aligned ClientHello, wss, ...).
	Camouflage bool
	// Heartbeat means ServeHeartbeat runs the shared application ping/pong
	// (heartbeat.go) on every dialed connection.
	Heartbeat bool
	// Multiplex means one tunnel connection carries several independent streams
	// (the P1 interface slot; no P0 plugin sets it).
	Multiplex bool
	// UDP means the plugin can carry UDP sessions (nothing sets it under the
	// P0 single-TCP-stream contract).
	UDP bool
}

// Capper is implemented by plugins that declare their capabilities (F-0822).
// The interface stays optional so third-party Tunnel implementations and test
// fakes keep compiling; CapsOf supplies the P0 baseline for them.
type Capper interface {
	Caps() Caps
}

// BaselineCaps is what an undeclaring plugin still guarantees under the P0
// contract: a byte-stream Dial plus the shared ping/pong heartbeat.
func BaselineCaps() Caps { return Caps{Heartbeat: true} }

// CapsOf returns a plugin's declaration, falling back to BaselineCaps when it
// does not implement Capper.
func CapsOf(t Tunnel) Caps {
	if c, ok := t.(Capper); ok {
		return c.Caps()
	}
	return BaselineCaps()
}
