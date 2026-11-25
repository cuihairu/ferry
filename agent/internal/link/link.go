// Package link maintains the outbound WebSocket long connection to the panel: dialing, exponential-backoff reconnect and message dispatch.
// The agent only makes outbound connections and opens no inbound ports on the host.
package link

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

// Handlers is the message entry point the link calls back into the upper layer (app).
type Handlers interface {
	// OnConnected is called after the connection is established and sends the handshake; the read loop is already
	// running by then, so the handshake reply (hello_ack) arrives via OnMessage; a returned error triggers a backoff reconnect.
	OnConnected(ctx context.Context, send func(agentproto.Envelope) error) error
	// OnMessage handles messages delivered by the panel.
	OnMessage(ctx context.Context, env agentproto.Envelope, send func(agentproto.Envelope) error)
	// OnDisconnected is called once after every disconnect.
	OnDisconnected()
}

// Options holds the link dial parameters.
type Options struct {
	URL        string        // wss:// or ws:// panel address
	CAFile     string        // self-signed CA; empty uses the system root certificates
	CertFile   string        // mTLS client certificate
	KeyFile    string        // mTLS client private key
	MinBackoff time.Duration // initial reconnect backoff, default 1s
	MaxBackoff time.Duration // maximum reconnect backoff, default 30s
	Log        *log.Logger
}

// Client is a reconnectable outbound connection.
type Client struct {
	opt Options
}

// New creates a client.
func New(opt Options) *Client {
	if opt.MinBackoff <= 0 {
		opt.MinBackoff = time.Second
	}
	if opt.MaxBackoff <= 0 {
		opt.MaxBackoff = 30 * time.Second
	}
	if opt.Log == nil {
		opt.Log = log.New(os.Stderr, "link ", log.LstdFlags)
	}
	return &Client{opt: opt}
}

// Run blocks until the ctx is cancelled: connect -> hand over to handlers -> disconnect -> backoff and reconnect.
func (c *Client) Run(ctx context.Context, h Handlers) error {
	backoff := c.opt.MinBackoff
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := c.connectOnce(ctx, h)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			c.opt.Log.Printf("connection ended: %v", err)
		}
		c.opt.Log.Printf("reconnect in %s", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > c.opt.MaxBackoff {
			backoff = c.opt.MaxBackoff
		}
	}
}

func (c *Client) connectOnce(ctx context.Context, h Handlers) error {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	tlsCfg, err := c.tlsConfig()
	if err != nil {
		return err
	}
	dialer.TLSClientConfig = tlsCfg

	conn, _, err := dialer.DialContext(ctx, c.opt.URL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	send := newSender(conn)
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// close the connection when the ctx ends, unblocking the in-flight read.
	go func() {
		<-connCtx.Done()
		conn.Close()
	}()

	// the read loop starts before OnConnected: the handshake reply (hello_ack) must be read off the socket
	// while OnConnected is blocking on it and delivered via OnMessage; a later start would time out every handshake.
	readErr := make(chan error, 1)
	go func() { readErr <- c.readLoop(connCtx, h, send, conn) }()
	if err := h.OnConnected(connCtx, send); err != nil {
		cancel() // closing the connection unblocks the read and the read loop exits with connCtx
		<-readErr
		return err
	}
	err = <-readErr
	h.OnDisconnected()
	return err
}

func (c *Client) tlsConfig() (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.opt.CAFile != "" {
		pem, err := os.ReadFile(c.opt.CAFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("no valid certificate in ca_file")
		}
		cfg.RootCAs = pool
	}
	if c.opt.CertFile != "" && c.opt.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.opt.CertFile, c.opt.KeyFile)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

func (c *Client) readLoop(ctx context.Context, h Handlers, send func(agentproto.Envelope) error, conn *websocket.Conn) error {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var env agentproto.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			c.opt.Log.Printf("drop malformed message: %v", err)
			continue
		}
		if !env.Valid() {
			c.opt.Log.Printf("drop message with version %d", env.V)
			continue
		}
		h.OnMessage(ctx, env, send)
	}
}

// sender serializes writes; a websocket connection does not allow concurrent writes from multiple goroutines.
func newSender(conn *websocket.Conn) func(agentproto.Envelope) error {
	var mu sync.Mutex
	return func(env agentproto.Envelope) error {
		raw, err := json.Marshal(env)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, raw)
	}
}
