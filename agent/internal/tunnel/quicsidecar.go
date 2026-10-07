package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// quic sidecar 插件（E-18b，拍板：独立 plugin 二进制）：本插件不携带任何
// QUIC 实现——只把连接经本地 CONNECT 桥交给 ferry-quic sidecar（quic-go 只
// 落那个二进制，agent 主程序尺寸门禁 E-4 不动）。部署：ferry-quic client
// 与 agent 同机运行（systemd/被管进程位），默认桥地址 127.0.0.1:7300，
// 可用环境变量 FERRY_QUIC_SIDECAR 覆盖。
//
// 诚实口径：经此插件的传输是 raw QUIC（ALPN ferry-quic），不是 hysteria2，
// 不冒称任何现有协议。
const pluginQUIC = "quic"

// SidecarEnv 是 sidecar 桥地址的环境变量名。
const SidecarEnv = "FERRY_QUIC_SIDECAR"

// DefaultSidecarAddr 是 sidecar 桥的缺省监听地址（与 ferry-quic client 缺省对齐）。
const DefaultSidecarAddr = "127.0.0.1:7300"

// SidecarAddr 归一 sidecar 桥地址。
func SidecarAddr() string {
	if v := strings.TrimSpace(os.Getenv(SidecarEnv)); v != "" {
		return v
	}
	return DefaultSidecarAddr
}

func init() {
	// 插件注册失败属于编程错误，直接 panic（init 期注册无并发）。
	if err := Register(pluginQUIC, func(o Options) (Tunnel, error) {
		return &quicSidecar{opts: o}, nil
	}); err != nil {
		panic(err)
	}
}

// quicSidecar 是 CONNECT 桥拨号实现：无状态，Close 空实现。
type quicSidecar struct{ opts Options }

func (t *quicSidecar) Name() string { return pluginQUIC }

func (t *quicSidecar) Dial(ctx context.Context, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: t.opts.TimeoutOrDefault()}
	conn, err := d.DialContext(ctx, "tcp", SidecarAddr())
	if err != nil {
		return nil, fmt.Errorf("tunnel quic: sidecar %s: %w", SidecarAddr(), err)
	}
	sni := t.opts.ServerName
	if sni == "" {
		host, _, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			host = addr
		}
		sni = host
	}
	// 请求行与应答都带超时，sidecar 不在/版本不符时快速失败不悬挂。
	_ = conn.SetDeadline(time.Now().Add(t.opts.TimeoutOrDefault()))
	if _, err := fmt.Fprintf(conn, "CONNECT %s sni=%s\n", addr, sni); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("tunnel quic: send connect: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("tunnel quic: read connect reply: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	switch {
	case strings.HasPrefix(line, "OK"):
		return conn, nil
	case strings.HasPrefix(line, "ERR"):
		_ = conn.Close()
		return nil, fmt.Errorf("tunnel quic: %s: %s", addr, strings.TrimSpace(strings.TrimPrefix(line, "ERR")))
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("tunnel quic: unexpected sidecar reply %q", line)
	}
}

func (t *quicSidecar) ServeHeartbeat(ctx context.Context, conn net.Conn, interval time.Duration) error {
	return PingPong(ctx, conn, interval)
}

func (t *quicSidecar) Close() error {
	// 无全局资源；sidecar 生命周期独立于 agent（systemd/被管进程位）。
	return nil
}
