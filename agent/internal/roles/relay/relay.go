// Package relay 是 relay 数据面组件（E-5）：入口角色的独立转发进程。
//
// 形态：本机监听 → 每条前端连接经传输插件（默认 tls-camo）拨落地 → 双向拷贝。
// 崩溃只断转发，核心心跳不受影响；被管方式是 procs 的一条普通规格
// （exec=ferry-agent，args=[-role, relay]），随面板 proc_ctl 启停。
//
// P0 口径：单流转发（传输插件 Dial 的连接即数据通道）；落地侧只需能终结
// 该传输（tls-camo 对端是任意 TLS 监听），无需 ferry 私有握手协议。
//
// 摘挂热更新换线（E-16b）：RunFile 支持落地覆盖文件——面板 config.push
// 把落地指向（RelaySpec 子集 JSON）写到该文件，SIGHUP 重读热切换，
// 新连接走新落地，存量连接按旧拨号自然排空（不硬断）。
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/tunnel"
)

// maxConns 是并发转发上限：入口小机器防爆，超限直接拒掉新连接。
const maxConns = 128

// dialTimeout 是单条转发连接的落地拨号超时。
const dialTimeout = 15 * time.Second

// Run 阻塞运行到 ctx 取消：监听 → 逐连接转发。
func Run(ctx context.Context, spec config.RelaySpec, logger *log.Logger) error {
	return run(ctx, spec, "", nil, nil, logger)
}

// RunFile 在 Run 之上启用落地覆盖文件（E-16b）：SIGHUP 重读 path 热切换。
func RunFile(ctx context.Context, base config.RelaySpec, path string, logger *log.Logger) error {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	return run(ctx, base, path, hup, nil, logger)
}

// run 是 Run/RunFile 的公共体：overridePath 非空时启用覆盖热重载；
// ln/hup 可注入供测试。启动前先打开现行传输插件（坏插件尽早暴露）。
func run(ctx context.Context, base config.RelaySpec, overridePath string, hup <-chan os.Signal, ln net.Listener, logger *log.Logger) error {
	if logger == nil {
		logger = log.Default()
	}
	if base.Listen == "" {
		return errors.New("relay: listen is required")
	}
	cur := base
	if overridePath != "" {
		ov, err := loadOverride(overridePath)
		if err != nil {
			return err // 文件存在但损坏：启动失败尽早暴露，不静默落回
		}
		cur = mergeSpec(base, ov)
	}
	if cur.LandingAddr == "" {
		return errors.New("relay: landing_addr is required")
	}

	tunnels := newTunnelCache(logger)
	defer tunnels.closeAll()
	if _, err := tunnels.get(cur); err != nil {
		return err
	}

	if ln == nil {
		var err error
		ln, err = net.Listen("tcp", base.Listen)
		if err != nil {
			return err
		}
		defer ln.Close()
	}
	logger.Printf("relay listening on %s -> %s via %s", base.Listen, cur.LandingAddr, tunnelPluginOf(cur))

	var curSpec atomic.Pointer[config.RelaySpec]
	curSpec.Store(&cur)

	// 热重载：SIGHUP 重读覆盖文件换落地指向；坏文件或空落地保现行只记日志。
	if overridePath != "" && hup != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-hup:
				}
				ov, err := loadOverride(overridePath)
				if err != nil {
					logger.Printf("relay override reload: %v", err)
					continue
				}
				next := mergeSpec(base, ov)
				if next.LandingAddr == "" {
					logger.Printf("relay override reload: landing_addr empty, keep current")
					continue
				}
				old := curSpec.Load()
				if next.LandingAddr == old.LandingAddr &&
					next.ServerName == old.ServerName && next.CAFile == old.CAFile &&
					tunnelPluginOf(next) == tunnelPluginOf(*old) {
					continue // 没变化不切，避免无谓日志
				}
				curSpec.Store(&next)
				logger.Printf("relay repointed -> %s via %s", next.LandingAddr, tunnelPluginOf(next))
			}
		}()
	}

	sem := make(chan struct{}, maxConns)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		front, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				logger.Printf("relay accept: %v", err)
				continue
			}
		}
		select {
		case sem <- struct{}{}:
		default:
			logger.Printf("relay overloaded, dropping %s", front.RemoteAddr())
			_ = front.Close()
			continue
		}
		go func(front net.Conn) {
			defer func() { <-sem }()
			defer front.Close()
			s := curSpec.Load()
			tn, err := tunnels.get(*s)
			if err != nil {
				logger.Printf("relay tunnel %s: %v", tunnelPluginOf(*s), err)
				return
			}
			serveConn(ctx, tn, s.LandingAddr, front, logger)
		}(front)
	}
}

// serveConn 为一条前端连接拨落地并双向拷贝；任一方向结束即断开整条。
func serveConn(ctx context.Context, tn tunnel.Tunnel, landing string, front net.Conn, logger *log.Logger) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	back, err := tn.Dial(dialCtx, landing)
	if err != nil {
		logger.Printf("relay dial %s: %v", landing, err)
		return
	}
	defer back.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(back, front)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(front, back)
	}()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

// mergeSpec 以 agent 本地配置为基底叠加面板覆盖（E-16b）：listen 等
// 本机参数不归面板管，只吃落地指向相关字段的非空值。ssh 四参是本机
// 文件引用与认证面（E-28），与 listen 同口径不归面板管（面板只换传输名）。
func mergeSpec(base, ov config.RelaySpec) config.RelaySpec {
	s := base
	if ov.LandingAddr != "" {
		s.LandingAddr = ov.LandingAddr
	}
	if ov.Tunnel != "" {
		s.Tunnel = ov.Tunnel
	}
	if ov.ServerName != "" {
		s.ServerName = ov.ServerName
	}
	if ov.CAFile != "" {
		s.CAFile = ov.CAFile
	}
	return s
}

// loadOverride 读覆盖文件；不存在属正常态（尚未下发过），返回零值不报错。
func loadOverride(path string) (config.RelaySpec, error) {
	var spec config.RelaySpec
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return spec, nil
	}
	if err != nil {
		return spec, err
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		return spec, err
	}
	return spec, nil
}

// tunnelPluginOf relay 传输插件名：空=缺省。
func tunnelPluginOf(spec config.RelaySpec) string {
	if spec.Tunnel == "" {
		return tunnel.DefaultPlugin
	}
	return spec.Tunnel
}

// tunnelCache 按插件与 TLS 参数缓存 Tunnel：热重载换落地不重开插件，
// 旧 Tunnel 由旧连接持有，进程退出统一关。
type tunnelCache struct {
	mu     sync.Mutex
	m      map[string]tunnel.Tunnel
	logger *log.Logger
}

func newTunnelCache(logger *log.Logger) *tunnelCache {
	return &tunnelCache{m: map[string]tunnel.Tunnel{}, logger: logger}
}

func (c *tunnelCache) get(spec config.RelaySpec) (tunnel.Tunnel, error) {
	// 缓存键含插件全部实例参数：同插件名不同参数（如 ssh 换用户/密钥/
	// 目标）必须分开实例，否则复用串配置。
	key := strings.Join([]string{
		tunnelPluginOf(spec), spec.ServerName, spec.CAFile,
		spec.SSHUser, spec.SSHKeyFile, spec.SSHKnownHosts, spec.SSHForwardAddr,
	}, "|")
	c.mu.Lock()
	defer c.mu.Unlock()
	if tn, ok := c.m[key]; ok {
		return tn, nil
	}
	tn, err := tunnel.Open(tunnelPluginOf(spec), tunnel.Options{
		ServerName:     spec.ServerName,
		CAFile:         spec.CAFile,
		Timeout:        dialTimeout,
		AuthUser:       spec.SSHUser,
		KeyFile:        spec.SSHKeyFile,
		KnownHostsFile: spec.SSHKnownHosts,
		ForwardAddr:    spec.SSHForwardAddr,
	})
	if err != nil {
		return nil, err
	}
	c.m[key] = tn
	return tn, nil
}

func (c *tunnelCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, tn := range c.m {
		if err := tn.Close(); err != nil {
			c.logger.Printf("relay close tunnel %s: %v", k, err)
		}
	}
	c.m = map[string]tunnel.Tunnel{}
}
