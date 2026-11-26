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

// defaultMaxConns is the fallback when RelaySpec.MaxConns is unset (F-0808).
const defaultMaxConns = maxConns

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
	// Counters (F-0815): the relay exposes its own metrics endpoint when
	// configured; the core process collector can scrape it via MetricsURL.
	mx := &relayMetrics{}
	serveMetrics(cur.MetricsListen, mx)

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
	// limit holds the current concurrent-forwarding ceiling; it starts at the base spec's
	// value and is refreshed on every successful hot reload.
	var limit atomic.Int64
	// Stream cap: 0 disables it (P0 single-stream forwarding); the ceiling is
	// refreshed on hot reload without touching in-flight dials.
	var streamLimit atomic.Int64
	var streams atomic.Int64
	streamLimit.Store(int64(effectiveMaxStreams(cur)))

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
					effectiveMaxConns(next) == effectiveMaxConns(*old) &&
					effectiveMaxStreams(next) == effectiveMaxStreams(*old) &&
					effectiveRateKbps(next) == effectiveRateKbps(*old) &&
					effectiveRetries(next) == effectiveRetries(*old) &&
					effectiveIdleTimeout(next) == effectiveIdleTimeout(*old) &&
					tunnelPluginOf(next) == tunnelPluginOf(*old) {
					continue // no change means no switch, avoiding pointless logs
				}
				curSpec.Store(&next)
				limit.Store(int64(effectiveMaxConns(next)))
				streamLimit.Store(effectiveMaxStreams(next))
				logger.Printf("relay repointed -> %s via %s (max_conns=%d)", next.LandingAddr, tunnelPluginOf(next), effectiveMaxConns(next))
			}
		}()
	}

	// The active count is atomic so a SIGHUP can change the ceiling without
	// dropping established connections or rebuilding a semaphore used by live goroutines.
	var active atomic.Int64
	limit.Store(int64(effectiveMaxConns(cur)))
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
		ceiling := int(limit.Load())
		if active.Add(1) > int64(ceiling) {
			active.Add(-1)
			mx.dropped.Add(1)
			logger.Printf("relay overloaded, dropping %s", front.RemoteAddr())
			_ = front.Close()
			continue
		}
		mx.conns.Add(1)
		go func(front net.Conn) {
			defer active.Add(-1)
			defer front.Close()
			s := curSpec.Load()
			tn, err := tunnels.get(*s)
			if err != nil {
				logger.Printf("relay tunnel %s: %v", tunnelPluginOf(*s), err)
				return
			}
			// Stream ceiling (F-0809): the backend dial is a distinct resource
			// from the frontend connection; when a plugin multiplexes streams
			// this cap still governs how many can be open concurrently.
			ceiling := int(streamLimit.Load())
			if ceiling > 0 && streams.Add(1) > int64(ceiling) {
				streams.Add(-1)
				mx.streamDrops.Add(1)
				logger.Printf("relay stream limit reached, dropping %s", front.RemoteAddr())
				return
			}
			if ceiling > 0 {
				defer streams.Add(-1)
			}
			serveConn(ctx, tn, s.LandingAddr, effectiveRateKbps(*s), effectiveRetries(*s),
				effectiveIdleTimeout(*s), front, logger, mx, func() {
					// F-0813: a failed dial invalidates the cached plugin instance;
					// the next connection rebuilds a fresh session.
					tunnels.drop(*s)
					_ = tn.Close()
				})
		}(front)
	}
}

// serveConn dials the landing for one frontend connection and copies both ways; either direction ending tears down the whole connection.
// A dial failure retries up to retries times (F-0813) after a short backoff;
// between attempts the cached tunnel is evicted so a broken session is rebuilt.
func serveConn(ctx context.Context, tn tunnel.Tunnel, landing string, rateKbps, retries int, idle time.Duration, front net.Conn, logger *log.Logger, mx *relayMetrics, onDialFail func()) {
	var (
		back net.Conn
		err  error
	)
	for attempt := 0; ; attempt++ {
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		back, err = tn.Dial(dialCtx, landing)
		cancel()
		if err == nil {
			mx.dials.Add(1)
			break
		}
		mx.dialFailures.Add(1)
		if onDialFail != nil {
			onDialFail()
		}
		if attempt >= retries {
			logger.Printf("relay dial %s after %d attempts: %v", landing, attempt+1, err)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryBackoff):
		}
	}
	defer back.Close()
	// Idle cleanup (F-0814): with a positive idle timeout both ends carry a
	// sliding read deadline refreshed on every successful read; a silent
	// connection hits the deadline and both ends are closed. Connections whose
	// transport cannot set deadlines (some plugin conns) are skipped rather
	// than failing the forward.
	if idle > 0 {
		front = &deadlineConn{Conn: front, idle: idle}
		back = &deadlineConn{Conn: back, idle: idle}
	}
	// Bandwidth limit (F-0810): applied per direction per connection; 0 means
	// unlimited. The limiter wraps the source side of io.Copy in both directions.
	fwd := newRateReader(front, rateKbps, time.Now)
	rev := newRateReader(back, rateKbps, time.Now)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(back, fwd)
		mx.bytesRx.Add(n)
	}()
	go func() {
		defer wg.Done()
		n, _ := io.Copy(front, rev)
		mx.bytesTx.Add(n)
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
	if ov.MaxConns > 0 {
		s.MaxConns = ov.MaxConns
	}
	if ov.MaxStreams > 0 {
		s.MaxStreams = ov.MaxStreams
	}
	if ov.RateLimitKbps > 0 {
		s.RateLimitKbps = ov.RateLimitKbps
	}
	if ov.DialRetries > 0 {
		s.DialRetries = ov.DialRetries
	}
	if ov.IdleTimeoutSec > 0 {
		s.IdleTimeoutSec = ov.IdleTimeoutSec
	}
	if ov.ServerName != "" {
		s.ServerName = ov.ServerName
	}
	if ov.CAFile != "" {
		s.CAFile = ov.CAFile
	}
	return s
}

// retryBackoff is the pause between landing dial attempts (F-0813).
const retryBackoff = 200 * time.Millisecond

// defaultDialRetries is one extra attempt after the first dial failure.
const defaultDialRetries = 1

// effectiveRetries normalizes the dial retry count: 0 takes the default.
func effectiveRetries(spec config.RelaySpec) int {
	if spec.DialRetries > 0 {
		return spec.DialRetries
	}
	return defaultDialRetries
}

// effectiveRateKbps normalizes the bandwidth ceiling: negatives mean unlimited.
func effectiveRateKbps(spec config.RelaySpec) int {
	if spec.RateLimitKbps > 0 {
		return spec.RateLimitKbps
	}
	return 0
}

// effectiveMaxStreams normalizes the stream ceiling: 0 (and negatives) means unlimited.
func effectiveMaxStreams(spec config.RelaySpec) int64 {
	if spec.MaxStreams > 0 {
		return int64(spec.MaxStreams)
	}
	return 0
}

// effectiveMaxConns normalizes the connection ceiling: 0 and negatives take the default.
func effectiveMaxConns(spec config.RelaySpec) int {
	if spec.MaxConns > 0 {
		return spec.MaxConns
	}
	return defaultMaxConns
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

// drop invalidates a cached plugin instance after a dial failure (F-0813): the
// next connection rebuilds it, so a broken connection/session is not reused
// forever. The caller owns closing the evicted instance.
func (c *tunnelCache) drop(spec config.RelaySpec) {
	key := strings.Join([]string{
		tunnelPluginOf(spec), spec.ServerName, spec.CAFile,
		spec.SSHUser, spec.SSHKeyFile, spec.SSHKnownHosts, spec.SSHForwardAddr,
	}, "|")
	c.mu.Lock()
	delete(c.m, key)
	c.mu.Unlock()
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
