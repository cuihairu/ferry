// Package relay is the relay data plane component (E-5): the standalone forwarding process of the entry role.
//
// Shape: listen locally -> each frontend connection dials the landing through a transport plugin (tls-camo by default) -> copy both ways.
// A crash only breaks forwarding, leaving the core heartbeat untouched; it is managed as an ordinary procs spec
// (exec=ferry-agent, args=[-role, relay]) started and stopped by panel proc_ctl.
//
// P0 rule: single-stream forwarding (the connection dialed by the transport plugin is the data channel); the landing side only needs to terminate
// that transport (the tls-camo peer is any TLS listener), with no ferry-private handshake protocol.
//
// Hot-swapping the landing (E-16b): RunFile supports a landing override file -- the panel's config.push
// writes the landing target (a RelaySpec subset in JSON) into that file; a SIGHUP re-reads it and hot-switches,
// so new connections use the new landing while existing ones drain naturally on the old dial (never hard-cut).
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/tunnel"
)

// maxConns caps concurrent forwarding: protects small entry machines, and new connections beyond the limit are refused outright.
const maxConns = 128

// dialTimeout is the landing dial timeout of one forwarded connection.
const dialTimeout = 15 * time.Second

// Run blocks until the ctx is cancelled: listen -> forward connection by connection.
func Run(ctx context.Context, spec config.RelaySpec, logger *log.Logger) error {
	return run(ctx, spec, "", nil, nil, logger)
}

// RunFile enables the landing override file on top of Run (E-16b): a SIGHUP re-reads path and hot-switches.
func RunFile(ctx context.Context, base config.RelaySpec, path string, logger *log.Logger) error {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	return run(ctx, base, path, hup, nil, logger)
}

// run is the common body of Run/RunFile: a non-empty overridePath enables hot-reloading the override;
// ln/hup are injectable for tests. The current transport plugin is opened before startup (bad plugins surface early).
func run(ctx context.Context, base config.RelaySpec, overridePath string, hup <-chan os.Signal, ln net.Listener, logger *log.Logger) error {
	if logger == nil {
		logger = log.Default()
	}
	if base.Listen == "" {
		return errors.New("relay: listen is required")
	}
	cur := base
	if overridePath != "" {
		ov, err := loadOverride(overridePath)
		if err != nil {
			return err // the file exists but is corrupt: fail startup early rather than silently falling back
		}
		cur = mergeSpec(base, ov)
	}
	if cur.LandingAddr == "" {
		return errors.New("relay: landing_addr is required")
	}

	tunnels := newTunnelCache(logger)
	defer tunnels.closeAll()
	if _, err := tunnels.get(cur); err != nil {
		return err
	}

	if ln == nil {
		var err error
		ln, err = net.Listen("tcp", base.Listen)
		if err != nil {
			return err
		}
		defer ln.Close()
	}
	logger.Printf("relay listening on %s -> %s via %s", base.Listen, cur.LandingAddr, tunnelPluginOf(cur))

	var curSpec atomic.Pointer[config.RelaySpec]
	curSpec.Store(&cur)

	// hot reload: a SIGHUP re-reads the override file to switch the landing target; a corrupt file or an empty landing keeps the current one and only logs.
	if overridePath != "" && hup != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-hup:
				}
				ov, err := loadOverride(overridePath)
				if err != nil {
					logger.Printf("relay override reload: %v", err)
					continue
				}
				next := mergeSpec(base, ov)
				if next.LandingAddr == "" {
					logger.Printf("relay override reload: landing_addr empty, keep current")
					continue
				}
				old := curSpec.Load()
				if next.LandingAddr == old.LandingAddr &&
					next.ServerName == old.ServerName && next.CAFile == old.CAFile &&
					tunnelPluginOf(next) == tunnelPluginOf(*old) {
					continue // no change means no switch, avoiding pointless logs
				}
				curSpec.Store(&next)
				logger.Printf("relay repointed -> %s via %s", next.LandingAddr, tunnelPluginOf(next))
			}
		}()
	}

	sem := make(chan struct{}, maxConns)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		front, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				logger.Printf("relay accept: %v", err)
				continue
			}
		}
		select {
		case sem <- struct{}{}:
		default:
			logger.Printf("relay overloaded, dropping %s", front.RemoteAddr())
			_ = front.Close()
			continue
		}
		go func(front net.Conn) {
			defer func() { <-sem }()
			defer front.Close()
			s := curSpec.Load()
			tn, err := tunnels.get(*s)
			if err != nil {
				logger.Printf("relay tunnel %s: %v", tunnelPluginOf(*s), err)
				return
			}
			serveConn(ctx, tn, s.LandingAddr, front, logger)
		}(front)
	}
}

// serveConn dials the landing for one frontend connection and copies both ways; either direction ending tears down the whole connection.
func serveConn(ctx context.Context, tn tunnel.Tunnel, landing string, front net.Conn, logger *log.Logger) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	back, err := tn.Dial(dialCtx, landing)
	if err != nil {
		logger.Printf("relay dial %s: %v", landing, err)
		return
	}
	defer back.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(back, front)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(front, back)
	}()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

// mergeSpec layers the panel override on top of the agent's local config (E-16b): local parameters such as
// listen are not the panel's business; only non-empty values of landing-related fields are taken. The four ssh parameters are a
// local file reference and authentication surface (E-28), treated like listen and not the panel's business (the panel only changes the transport name).
func mergeSpec(base, ov config.RelaySpec) config.RelaySpec {
	s := base
	if ov.LandingAddr != "" {
		s.LandingAddr = ov.LandingAddr
	}
	if ov.Tunnel != "" {
		s.Tunnel = ov.Tunnel
	}
	if ov.ServerName != "" {
		s.ServerName = ov.ServerName
	}
	if ov.CAFile != "" {
		s.CAFile = ov.CAFile
	}
	return s
}

// loadOverride reads the override file; a missing file is a normal state (nothing has been pushed yet) and returns zero values without an error.
func loadOverride(path string) (config.RelaySpec, error) {
	var spec config.RelaySpec
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return spec, nil
	}
	if err != nil {
		return spec, err
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		return spec, err
	}
	return spec, nil
}

// tunnelPluginOf maps the relay transport plugin name: empty = the default.
func tunnelPluginOf(spec config.RelaySpec) string {
	if spec.Tunnel == "" {
		return tunnel.DefaultPlugin
	}
	return spec.Tunnel
}

// tunnelCache caches Tunnels by plugin and TLS parameters: hot-reloading a landing does not reopen the plugin,
// the old Tunnel stays held by old connections, and everything is closed when the process exits.
type tunnelCache struct {
	mu     sync.Mutex
	m      map[string]tunnel.Tunnel
	logger *log.Logger
}

func newTunnelCache(logger *log.Logger) *tunnelCache {
	return &tunnelCache{m: map[string]tunnel.Tunnel{}, logger: logger}
}

func (c *tunnelCache) get(spec config.RelaySpec) (tunnel.Tunnel, error) {
	// the cache key contains every instance parameter of the plugin: the same plugin name with different parameters (ssh with a different user/key/
	// target) must be separate instances, otherwise reuse would cross the configurations.
	key := strings.Join([]string{
		tunnelPluginOf(spec), spec.ServerName, spec.CAFile,
		spec.SSHUser, spec.SSHKeyFile, spec.SSHKnownHosts, spec.SSHForwardAddr,
	}, "|")
	c.mu.Lock()
	defer c.mu.Unlock()
	if tn, ok := c.m[key]; ok {
		return tn, nil
	}
	tn, err := tunnel.Open(tunnelPluginOf(spec), tunnel.Options{
		ServerName:     spec.ServerName,
		CAFile:         spec.CAFile,
		Timeout:        dialTimeout,
		AuthUser:       spec.SSHUser,
		KeyFile:        spec.SSHKeyFile,
		KnownHostsFile: spec.SSHKnownHosts,
		ForwardAddr:    spec.SSHForwardAddr,
	})
	if err != nil {
		return nil, err
	}
	c.m[key] = tn
	return tn, nil
}

func (c *tunnelCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, tn := range c.m {
		if err := tn.Close(); err != nil {
			c.logger.Printf("relay close tunnel %s: %v", k, err)
		}
	}
	c.m = map[string]tunnel.Tunnel{}
}
