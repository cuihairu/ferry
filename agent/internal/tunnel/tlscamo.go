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

// tls 伪装插件（E-6 默认插件）：出站即 TLS，ClientHello 与浏览器对齐
// （TLS1.2+、ALPN h2/http1.1、SNI 取伪装域名），在网关视角里就是一次
// 普通 HTTPS 建连。服务端只需能终结 TLS（面板/落地 relay 监听器）。
const pluginTLSCamo = "tls-camo"

func init() {
	// 默认插件注册失败属于编程错误，直接 panic（init 期注册无并发）。
	if err := Register(pluginTLSCamo, func(o Options) (Tunnel, error) {
		return &tlsCamo{opts: o}, nil
	}); err != nil {
		panic(err)
	}
}

// DefaultPlugin 是面板/relay 缺省传输插件名。
const DefaultPlugin = pluginTLSCamo

// tlsCamo 是 TLS 伪装拨号实现：无状态，Close 空实现。
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
		// 浏览器对齐：优先 h2，与主流 HTTPS 建连无异。
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
