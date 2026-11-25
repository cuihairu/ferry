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
			return nil, fmt.Errorf("tunnel: read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("tunnel: no certificates in %s", t.opts.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	d := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: t.opts.TimeoutOrDefault()},
		Config:    tlsCfg,
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (t *tlsCamo) ServeHeartbeat(ctx context.Context, conn net.Conn, interval time.Duration) error {
	return PingPong(ctx, conn, interval)
}

func (t *tlsCamo) Close() error { return nil }
