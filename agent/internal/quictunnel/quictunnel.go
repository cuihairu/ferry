// Package quictunnel is the core of the standalone QUIC transport sidecar (E-18b, decision: a separate
// plugin binary): quic-go (MIT) lives only in the ferry-quic binary and the agent proper carries none of it
// -- the gate rule (E-4 10MB) stays untouched.
//
// Protocol shape: an extremely thin CONNECT bridge on the local side (the in-process quic plugin dials 127.0.0.1);
// the ferry-quic client translates the CONNECT target into a QUIC stream, and the server forwards each stream to
// the local target port. Bytes inside a stream are not interpreted (decoupled from application-layer heartbeats, the same rule as tls-camo).
package quictunnel

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// ALPNIdent is this transport's ALPN identifier: client and server must match for the handshake to succeed.
// It does not pose as any existing protocol (hysteria2 etc.); the docs honestly label it raw QUIC.
const ALPNIdent = "ferry-quic"

// connectPrefix is the request line prefix of the local bridging protocol (CONNECT host:port[ sni=name]).
const connectPrefix = "CONNECT "

// ErrRejected is the unified error for the sidecar refusing to open a stream (peer unreachable etc.).
var ErrRejected = errors.New("quictunnel: sidecar rejected")

// ParseConnectLine parses a CONNECT request line and returns the target address and the SNI (defaulting to the target host).
func ParseConnectLine(line string) (addr, sni string, err error) {
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, connectPrefix) {
		return "", "", fmt.Errorf("expect %q request, got %q", connectPrefix, line)
	}
	fields := strings.Fields(strings.TrimPrefix(line, connectPrefix))
	if len(fields) == 0 || fields[0] == "" {
		return "", "", errors.New("missing target address")
	}
	addr = fields[0]
	sni = ""
	for _, f := range fields[1:] {
		if v, ok := strings.CutPrefix(f, "sni="); ok && v != "" {
			sni = v
		}
	}
	if sni == "" {
		host, _, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			host = addr
		}
		sni = host
	}
	return addr, sni, nil
}

// readConnectLine reads one CONNECT request line from the connection (with a timeout so the local side cannot hang).
func readConnectLine(conn net.Conn, timeout time.Duration) (addr, sni string, err error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", "", err
	}
	_ = conn.SetReadDeadline(time.Time{})
	return ParseConnectLine(line)
}

// writeOK/writeErr answer a local bridging request line.
func writeOK(conn net.Conn) { _, _ = io.WriteString(conn, "OK\n") }

func writeErr(conn net.Conn, msg string) {
	_, _ = fmt.Fprintf(conn, "ERR %s\n", strings.ReplaceAll(msg, "\n", " "))
}

// pipeStreams copies both ways and closes both ends when either direction ends (the same semantics as relay serveConn).
func pipeStreams(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(b, a); _ = b.Close(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(a, b); _ = a.Close(); done <- struct{}{} }()
	<-done
}

// DefaultQUICConfig is the QUIC configuration shared by both ends: keepalive holds the NAT open, 2 minutes idle reclaims it,
// and per-connection stream ceilings bound what one bridge connection can open (F-0827).
func DefaultQUICConfig() *quic.Config {
	return &quic.Config{
		KeepAlivePeriod:       30 * time.Second,
		MaxIdleTimeout:        120 * time.Second,
		MaxIncomingStreams:    256, // concurrent forwarded streams per connection
		MaxIncomingUniStreams: 64,  // unidirectional headroom for quic-go control frames
	}
}

// ValidateQUICConfig checks the parameter invariants (F-0827): a positive idle
// timeout, keepalive strictly inside it (a keepalive at or past the idle
// timeout would fight the reclaim), and a bounded inbound stream ceiling.
func ValidateQUICConfig(cfg *quic.Config) error {
	switch {
	case cfg == nil:
		return errors.New("quic config missing")
	case cfg.MaxIdleTimeout <= 0:
		return fmt.Errorf("max_idle_timeout must be positive, got %v", cfg.MaxIdleTimeout)
	case cfg.KeepAlivePeriod < 0:
		return fmt.Errorf("keepalive_period must not be negative, got %v", cfg.KeepAlivePeriod)
	case cfg.KeepAlivePeriod >= cfg.MaxIdleTimeout:
		return fmt.Errorf("keepalive_period %v must stay under max_idle_timeout %v", cfg.KeepAlivePeriod, cfg.MaxIdleTimeout)
	case cfg.MaxIncomingStreams <= 0:
		return fmt.Errorf("max_incoming_streams must be positive, got %d", cfg.MaxIncomingStreams)
	}
	return nil
}

// ---- client (the agent-side sidecar) ----

// Client is the ferry-quic client mode: a local TCP listener receives CONNECT bridging requests,
// dials QUIC per target (connections are cached and reused by "address|SNI") and opens a stream per request to forward it.
type Client struct {
	tlsCfg  *tls.Config // the base configuration (NextProtos/root certificates/InsecureSkipVerify); each dial clones it and fills in the SNI
	quicCfg *quic.Config

	mu    sync.Mutex
	conns map[string]*quic.Conn
}

// NewClient validates and assembles a client: tlsCfg gets its NextProtos rewritten, so pass a dedicated instance.
// A bad QUIC configuration is a programming error -- fail fast at startup.
func NewClient(tlsCfg *tls.Config) *Client {
	cfg := DefaultQUICConfig()
	if err := ValidateQUICConfig(cfg); err != nil {
		panic(err)
	}
	tlsCfg.NextProtos = []string{ALPNIdent}
	return &Client{tlsCfg: tlsCfg, quicCfg: cfg, conns: map[string]*quic.Conn{}}
}

// NewClientWithConfig builds a client with an explicit QUIC configuration,
// validated against the F-0827 invariants.
func NewClientWithConfig(tlsCfg *tls.Config, cfg *quic.Config) (*Client, error) {
	if err := ValidateQUICConfig(cfg); err != nil {
		return nil, err
	}
	tlsCfg.NextProtos = []string{ALPNIdent}
	return &Client{tlsCfg: tlsCfg, quicCfg: cfg, conns: map[string]*quic.Conn{}}, nil
}

// Serve accepts local connections on ln until ln closes or the ctx is cancelled; a single failure is logged and service continues.
func (c *Client) Serve(ctx context.Context, ln net.Listener, logger *log.Logger) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		c.closeAll()
	}()
	for {
		local, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go func(local net.Conn) {
			if err := c.serveOne(ctx, local); err != nil && logger != nil {
				logger.Printf("ferry-quic client: %v", err)
			}
		}(local)
	}
}

// serveOne handles one local bridging: parse CONNECT -> fetch/build a QUIC connection -> open a stream -> join them.
// a cached connection may already have been silently discarded by the peer, so an open-stream failure evicts the cache entry and retries with a fresh dial.
func (c *Client) serveOne(ctx context.Context, local net.Conn) error {
	defer local.Close()
	addr, sni, err := readConnectLine(local, 10*time.Second)
	if err != nil {
		return fmt.Errorf("read connect: %w", err)
	}
	stream, err := c.openStream(ctx, addr, sni)
	if err != nil {
		writeErr(local, err.Error())
		return fmt.Errorf("open stream to %s: %w", addr, err)
	}
	writeOK(local)
	pipeStreams(local, stream) // quic.Stream implements net.Conn
	return nil
}

// openStream takes a cached QUIC connection and opens a stream; on failure it clears the cache and dials once more.
// the fresh dial's handshake carries a 10s timeout: a dead peer port (UDP sends no RST) must fail fast so the local bridge never hangs.
func (c *Client) openStream(ctx context.Context, addr, sni string) (*quic.Stream, error) {
	key := addr + "|" + sni
	if qc := c.get(key); qc != nil {
		if st, err := qc.OpenStreamSync(ctx); err == nil {
			return st, nil
		}
		c.drop(key, qc)
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	qc, err := c.dial(dialCtx, addr, sni)
	if err != nil {
		return nil, err
	}
	st, err := qc.OpenStreamSync(ctx)
	if err != nil {
		c.drop(key, qc)
		return nil, err
	}
	return st, nil
}

func (c *Client) dial(ctx context.Context, addr, sni string) (*quic.Conn, error) {
	tlsCfg := c.tlsCfg.Clone()
	tlsCfg.ServerName = sni
	qc, err := quic.DialAddr(ctx, addr, tlsCfg, c.quicCfg)
	if err != nil {
		return nil, err
	}
	key := addr + "|" + sni
	c.mu.Lock()
	c.conns[key] = qc
	c.mu.Unlock()
	return qc, nil
}

func (c *Client) get(key string) *quic.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conns[key]
}

func (c *Client) drop(key string, qc *quic.Conn) {
	c.mu.Lock()
	if c.conns[key] == qc {
		delete(c.conns, key)
	}
	c.mu.Unlock()
	_ = qc.CloseWithError(0, "stale")
}

func (c *Client) closeAll() {
	c.mu.Lock()
	conns := c.conns
	c.conns = map[string]*quic.Conn{}
	c.mu.Unlock()
	for _, qc := range conns {
		_ = qc.CloseWithError(0, "shutdown")
	}
}

// ---- server (the landing side) ----

// ServeServer is the ferry-quic server mode: a QUIC listener (UDP) accepts streams one by one and
// forwards them to the local target (TCP). Bytes inside a stream are not interpreted.
func ServeServer(ctx context.Context, ln *quic.Listener, target string, logger *log.Logger) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		qc, err := ln.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go func(qc *quic.Conn) {
			for {
				stream, err := qc.AcceptStream(ctx)
				if err != nil {
					return
				}
				go forwardStream(stream, target, logger)
			}
		}(qc)
	}
}

// forwardStream joins one QUIC stream <-> target TCP; a failed target dial closes the stream.
func forwardStream(stream *quic.Stream, target string, logger *log.Logger) {
	defer stream.Close()
	tcp, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		if logger != nil {
			logger.Printf("ferry-quic server: dial target %s: %v", target, err)
		}
		return
	}
	pipeStreams(stream, tcp)
}
