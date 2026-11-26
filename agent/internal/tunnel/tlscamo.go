package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"time"
)

// The tls camouflage plugin (the E-6 default plugin): outbound traffic is TLS with a ClientHello aligned to browsers
// (TLS1.2+, ALPN h2/http1.1, the SNI taken from the camouflage domain), which in a gateway's view is just an
// ordinary HTTPS connection setup. The server only needs to be able to terminate TLS (the panel / landing relay listener).
const pluginTLSCamo = "tls-camo"

func init() {
	// the default plugin failing to register is a programming error, so panic directly (init-time registration has no concurrency).
	if err := Register(pluginTLSCamo, func(o Options) (Tunnel, error) {
		return &tlsCamo{opts: o}, nil
	}); err != nil {
		panic(err)
	}
}

// DefaultPlugin is the panel/relay default transport plugin name.
const DefaultPlugin = pluginTLSCamo

// tlsCamo is the TLS camouflage dialer: stateless, with an empty Close.
type tlsCamo struct {
	opts Options
}

func (t *tlsCamo) Name() string { return pluginTLSCamo }

func (t *tlsCamo) Dial(ctx context.Context, addr string) (net.Conn, error) {
	serverName := t.opts.ServerName
	if serverName == "" {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		serverName = host
	}
	tlsCfg := &tls.Config{
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
		// browser aligned: prefer h2, indistinguishable from a mainstream HTTPS connection setup.
		NextProtos: []string{"h2", "http/1.1"},
	}
	if t.opts.CAFile != "" {
		pemBytes, err := os.ReadFile(t.opts.CAFile)
		if err != nil {
			return nil, Wrap(ClassConfig, "tls-camo:dial", fmt.Errorf("read CA file: %w", err))
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, Errorf(ClassConfig, "tls-camo:dial", "no certificates in %s", t.opts.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	d := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: t.opts.TimeoutOrDefault()},
		Config:    tlsCfg,
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		// tls.Dialer folds DNS/TCP/TLS trouble into one error; the retryable
		// network class is the honest bucket (certificate rejection is
		// indistinguishable from a refused connection here).
		return nil, Wrap(ClassNetwork, "tls-camo:dial", err)
	}
	return conn, nil
}

func (t *tlsCamo) ServeHeartbeat(ctx context.Context, conn net.Conn, interval time.Duration) error {
	return PingPong(ctx, conn, interval)
}

// Caps declares the plugin's features (F-0822): browser-aligned TLS camouflage
// plus the shared ping/pong; no multiplexing or UDP under the P0 contract.
func (t *tlsCamo) Caps() Caps {
	return Caps{Camouflage: true, Heartbeat: true}
}

func (t *tlsCamo) Close() error { return nil }
