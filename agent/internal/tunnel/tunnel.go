// Package tunnel 是传输插件抽象（E-6）：隧道建立与传输实现解耦。
//
// P0 口径：单流 + 应用层 ping/pong 心跳；多路复用（单连接多流）为 P1 扩展位，
// 接口暂不预留 OpenStream——需要时由插件自行在 Dial 返回的连接上做封装。
package tunnel

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// Tunnel 是传输插件接口：建立出站隧道连接，连接上跑应用心跳。
type Tunnel interface {
	// Name 是插件名（注册表键）。
	Name() string
	// Dial 建立一条出站连接（插件负责流量伪装）；ctx 取消则放弃。
	Dial(ctx context.Context, addr string) (net.Conn, error)
	// ServeHeartbeat 在已建立的连接上跑 ping/pong 心跳直到 ctx 取消或
	// 连接断开；返回 nil 表示 ctx 正常结束，非 nil 由调用方决定重连。
	ServeHeartbeat(ctx context.Context, conn net.Conn, interval time.Duration) error
	// Close 释放插件持有的全局资源（监听器等，纯拨号插件可空实现）。
	Close() error
}

// Factory 按选项构造插件实例。
type Factory func(Options) (Tunnel, error)

var (
	mu       sync.Mutex
	registry = map[string]Factory{}
)

// Register 注册传输插件（重复注册返回错误，尽早暴露插件名冲突）。
func Register(name string, f Factory) error {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[name]; dup {
		return fmt.Errorf("tunnel plugin %q already registered", name)
	}
	registry[name] = f
	return nil
}

// Open 按名构造插件；未知名返回错误（调用方回落默认插件或报错，而非静默裸连）。
func Open(name string, opts Options) (Tunnel, error) {
	mu.Lock()
	f, ok := registry[name]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown tunnel plugin %q", name)
	}
	return f(opts)
}

// Names 返回已注册插件名（排序不保证，展示用）。
func Names() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	return out
}

// Options 是插件通用选项：伪装目标与超时。
type Options struct {
	// ServerName 是 TLS SNI/伪装域名（空则直连地址本身，不伪装）。
	// ssh 插件语义：sshd 地址（host:port）。
	ServerName string
	// Timeout 是单次 Dial 超时，0 取 DefaultDialTimeout。
	Timeout time.Duration
	// CAFile 是私有 CA 证书路径（空则用系统根证书；mTLS 部署与自建 CA 走这里）。
	CAFile string

	// 以下字段仅 ssh 插件（E-28）使用，其他插件忽略。

	// AuthUser 是 SSH 登录用户（publickey 单一认证形态，无密码登录面）。
	AuthUser string
	// KeyFile 是私钥文件路径（agent 本机路径引用，密钥内容不进配置不进库）。
	KeyFile string
	// KnownHostsFile 是 OpenSSH 格式 known_hosts 路径（host key 校验）；
	// 空=跳过校验，仅引导期可用，生产必须配置。
	KnownHostsFile string
	// ForwardAddr 是 direct-tcpip 目标：落地本机 ferry relay 监听地址
	// （如 127.0.0.1:10800），sshd 收 channel 后拨本机。
	ForwardAddr string
}

// DefaultDialTimeout 是 Dial 缺省超时。
const DefaultDialTimeout = 10 * time.Second

// TimeoutOrDefault 归一化超时。
func (o Options) TimeoutOrDefault() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return DefaultDialTimeout
}
