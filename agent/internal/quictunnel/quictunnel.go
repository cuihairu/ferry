// Package quictunnel 是 QUIC 传输的独立 sidecar 核心（E-18b，拍板：独立
// plugin 二进制）：quic-go（MIT）只落 ferry-quic 一个二进制，agent 主程序
// 不携带——门禁口径（E-4 10MB）不动。
//
// 协议形态：本地侧一段极薄的 CONNECT 桥（进程内 quic 插件拨 127.0.0.1），
// ferry-quic client 把 CONNECT 目标翻译成 QUIC 流；server 侧逐流转发给
// 本机 target 端口。流内字节不解释（与应用层心跳解耦，与 tls-camo 同口径）。
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

// ALPNIdent 是本传输的 ALPN 标识：客户端与服务端必须一致才握手成功，
// 不冒称任何现有协议（hysteria2 等），文档如实标注 raw QUIC。
const ALPNIdent = "ferry-quic"

// connectPrefix 是本地桥接协议的请求行前缀（CONNECT host:port[ sni=name]）。
const connectPrefix = "CONNECT "

// ErrRejected 是 sidecar 拒绝建流（对端不可达等）的统一错误。
var ErrRejected = errors.New("quictunnel: sidecar rejected")

// ParseConnectLine 解析 CONNECT 请求行，返回目标地址与 SNI（缺省取目标 host）。
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

// readConnectLine 从连接上读一行 CONNECT 请求（带超时，防本地悬挂）。
func readConnectLine(conn net.Conn, timeout time.Duration) (addr, sni string, err error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", "", err
	}
	_ = conn.SetReadDeadline(time.Time{})
	return ParseConnectLine(line)
}

// writeOK/writeErr 应答本地桥接请求行。
func writeOK(conn net.Conn) { _, _ = io.WriteString(conn, "OK\n") }

func writeErr(conn net.Conn, msg string) {
	_, _ = fmt.Fprintf(conn, "ERR %s\n", strings.ReplaceAll(msg, "\n", " "))
}

// pipeStreams 双向拷贝，任一方向结束即关两端（relay serveConn 同语义）。
func pipeStreams(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(b, a); _ = b.Close(); done <- struct{}{} }()
	go func() { _, _ = io.Copy(a, b); _ = a.Close(); done <- struct{}{} }()
	<-done
}

// DefaultQUICConfig 是两端共用的 QUIC 参数：keepalive 顶住 NAT，空闲 2 分钟回收。
func DefaultQUICConfig() *quic.Config {
	return &quic.Config{
		KeepAlivePeriod: 30 * time.Second,
		MaxIdleTimeout:  120 * time.Second,
	}
}

// ---- client（agent 侧 sidecar）----

// Client 是 ferry-quic client 模式：本地 TCP 监听收 CONNECT 桥接请求，
// 按目标拨 QUIC（连接按「地址|SNI」缓存复用）并逐条开流转发。
type Client struct {
	tlsCfg  *tls.Config // 基础配置（NextProtos/根证书/InsecureSkipVerify），按拨号克隆补 SNI
	quicCfg *quic.Config

	mu    sync.Mutex
	conns map[string]*quic.Conn
}

// NewClient 校验并装配 client：tlsCfg 会被改写 NextProtos，请传入专用实例。
func NewClient(tlsCfg *tls.Config) *Client {
	tlsCfg.NextProtos = []string{ALPNIdent}
	return &Client{tlsCfg: tlsCfg, quicCfg: DefaultQUICConfig(), conns: map[string]*quic.Conn{}}
}

// Serve 在 ln 上接受本地连接直到 ln 关闭或 ctx 取消；单条失败只记日志不断服。
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

// serveOne 处理一条本地桥接：解析 CONNECT → 取/建 QUIC 连接 → 开流 → 打通。
// 缓存连接可能已被对端静默回收，开流失败回收缓存后重试一次新拨。
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
	pipeStreams(local, stream) // quic.Stream 实现了 net.Conn
	return nil
}

// openStream 取缓存 QUIC 连接并开流；失败清缓存重拨一次。
// 新拨握手带 10s 超时：对端死端口（UDP 无 RST）也要快速失败，不悬挂本地桥。
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

// ---- server（落地侧）----

// ServeServer 是 ferry-quic server 模式：QUIC 监听（UDP），逐条接受流并
// 转发给本机 target（TCP）。流内字节不解释。
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

// forwardStream 一条 QUIC 流 ↔ target TCP；target 拨号失败即关流。
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
