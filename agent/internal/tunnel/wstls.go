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

// ws-tls 传输插件（E-18）：出站 TLS + WebSocket 升级，数据走二进制帧。
// 升级握手与浏览器同形（Origin/UA），网关视角是一次普通 WS-Secure 建连，
// 可前置云 CDN。落地侧是任意标准 RFC6455 WebSocket 监听（二进制载荷，
// 路径任意）；服务端 ping 由库在读路径默认 pong。
const pluginWSTLS = "ws-tls"

func init() {
	// 插件注册失败属于编程错误，直接 panic（init 期注册无并发）。
	if err := Register(pluginWSTLS, func(o Options) (Tunnel, error) {
		return &wsTls{opts: o}, nil
	}); err != nil {
		panic(err)
	}
}

// wsTls 是 WS over TLS 拨号实现：无状态，Close 空实现。
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
		// WS 升级走 HTTP/1.1，与主流 WS 客户端无异。
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

// wsConn 把 WebSocket 消息语义适配成流语义的 net.Conn（relay 的 io.Copy
// 与应用心跳都按字节流工作）：写侧每条 Write 一个二进制消息；读侧跨
// 消息续读不限长。库口径一个连接同时只允许一个读方与一个写方，分别加锁。
type wsConn struct {
	ws  *websocket.Conn
	wmu sync.Mutex
	rmu sync.Mutex
	rd  io.Reader // 当前消息的剩余流，nil 表示需要 NextReader
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
				continue // 控制帧由库处理，其余非二进制消息不交给上层
			}
			c.rd = r
		}
		n, err := c.rd.Read(p)
		if err == io.EOF {
			c.rd = nil // 本条消息读尽，续下一条
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
