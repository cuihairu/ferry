// Package link 维护与面板的出站 WebSocket 长连接：拨号、指数退避重连、收发分发。
// agent 只出站连接，不在本机开任何入站端口。
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

// Handlers 是 link 回调给上层（app）的消息入口。
type Handlers interface {
	// OnConnected 在连接建立后调用，用于发送握手；期间读循环已在运行，
	// 握手应答（hello_ack）经 OnMessage 送达；返回错误触发退避重连。
	OnConnected(ctx context.Context, send func(agentproto.Envelope) error) error
	// OnMessage 处理面板下发的消息。
	OnMessage(ctx context.Context, env agentproto.Envelope, send func(agentproto.Envelope) error)
	// OnDisconnected 在每次连接断开后调用一次。
	OnDisconnected()
}

// Options 是 link 的拨号参数。
type Options struct {
	URL        string        // wss:// 或 ws:// 面板地址
	CAFile     string        // 自签 CA，空则用系统根证书
	CertFile   string        // mTLS 客户端证书
	KeyFile    string        // mTLS 客户端私钥
	MinBackoff time.Duration // 重连起始退避，默认 1s
	MaxBackoff time.Duration // 重连上限退避，默认 30s
	Log        *log.Logger
}

// Client 是可重连的出站连接。
type Client struct {
	opt Options
}

// New 创建 client。
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

// Run 阻塞运行直到 ctx 取消：连接 → 交给 handlers → 断开 → 退避重连。
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

	// ctx 结束时关连接，解除阻塞中的读。
	go func() {
		<-connCtx.Done()
		conn.Close()
	}()

	// 读循环先于 OnConnected 启动：握手应答（hello_ack）要在 OnConnected
	// 阻塞等待期间被读进来并经 OnMessage 送达，后启动会让握手必超时。
	readErr := make(chan error, 1)
	go func() { readErr <- c.readLoop(connCtx, h, send, conn) }()
	if err := h.OnConnected(connCtx, send); err != nil {
		cancel() // 关连接解除读阻塞，读循环随 connCtx 退出
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

// sender 串行化写操作，websocket 连接不允许多 goroutine 并发写。
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
