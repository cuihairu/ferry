package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// The ws-tls transport plugin (E-18): outbound TLS plus a WebSocket upgrade, with data in binary frames.
// The upgrade handshake looks like a browser's (Origin/UA), so a gateway sees an ordinary WS-Secure connection setup,
// which a cloud CDN can front. The landing side is any standard RFC6455 WebSocket listener (binary payloads,
// any path); server pings are answered with a pong by the library on the read path.
const pluginWSTLS = "ws-tls"

func init() {
	// a plugin failing to register is a programming error, so panic directly (init-time registration has no concurrency).
	if err := Register(pluginWSTLS, func(o Options) (Tunnel, error) {
		return &wsTls{opts: o}, nil
	}); err != nil {
		panic(err)
	}
}

// wsTls is the WS over TLS dialer: stateless, with an empty Close.
type wsTls struct{ opts Options }

func (t *wsTls) Name() string { return pluginWSTLS }

func (t *wsTls) Dial(ctx context.Context, addr string) (net.Conn, error) {
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
		// the WS upgrade goes over HTTP/1.1, indistinguishable from mainstream WS clients.
		NextProtos: []string{"http/1.1"},
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
	d := &websocket.Dialer{
		TLSClientConfig:  tlsCfg,
		HandshakeTimeout: t.opts.TimeoutOrDefault(),
	}
	header := http.Header{
		"Origin":     {"https://" + serverName},
		"User-Agent": {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"},
	}
	ws, _, err := d.DialContext(ctx, "wss://"+addr+"/ferry", header)
	if err != nil {
		return nil, fmt.Errorf("ws-tls dial %s: %w", addr, err)
	}
	return &wsConn{ws: ws}, nil
}

func (t *wsTls) ServeHeartbeat(ctx context.Context, conn net.Conn, interval time.Duration) error {
	return PingPong(ctx, conn, interval)
}

func (t *wsTls) Close() error { return nil }

// wsConn adapts WebSocket message semantics into the stream semantics of a net.Conn (relay's io.Copy
// and application heartbeats both work on byte streams): the write side sends one binary message per Write; the read side keeps
// reading across messages with no length cap. The library allows one reader and one writer per connection, each locked separately.
type wsConn struct {
	ws  *websocket.Conn
	wmu sync.Mutex
	rmu sync.Mutex
	rd  io.Reader // the remainder of the current message's stream; nil means NextReader is needed
}

func (c *wsConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if c.rd == nil {
			t, r, err := c.ws.NextReader()
			if err != nil {
				return 0, err
			}
			if t != websocket.BinaryMessage {
				continue // the library handles control frames; other non-binary messages are not handed to the upper layer
			}
			c.rd = r
		}
		n, err := c.rd.Read(p)
		if err == io.EOF {
			c.rd = nil // this message is exhausted, continue with the next
			continue
		}
		return n, err
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	w, err := c.ws.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, err
	}
	if _, err := w.Write(p); err != nil {
		_ = w.Close()
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error         { return c.ws.Close() }
func (c *wsConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.ws.SetReadDeadline(t); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(t)
}

func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }
