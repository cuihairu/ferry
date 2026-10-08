package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ssh 传输插件（E-28，传输插件矩阵「备选」行）：标准 SSH2 出站到落地机
// sshd，publickey 认证，direct-tcpip channel 拨落地本机的 ferry relay
// 监听——流量形态就是一次普通 SSH 会话内的端口转发（同 ssh -L 的远端侧），
// sshd 原生终结。特征独特易被识别，只作备选不作默认（设计 §传输插件矩阵）。
// 凭证口径：密钥文件路径引用（agent 本机文件，不进库）；known_hosts 文件
// 引用做 host key 校验，空=跳过校验（仅引导期，生产必须配置）。
const pluginSSH = "ssh"

func init() {
	// 插件注册失败属于编程错误，直接 panic（init 期注册无并发）。
	if err := Register(pluginSSH, func(o Options) (Tunnel, error) {
		return &sshTunnel{opts: o}, nil
	}); err != nil {
		panic(err)
	}
}

// sshTunnel 持单条 SSH 复用连接（一个 client 多 channel，与 ssh -L 常驻
// 同形）：Dial 失败先判定连接死掉重连一次再试；Close 关复用连接。
type sshTunnel struct {
	opts    Options
	mu      sync.Mutex
	sshConn *ssh.Client
	warned  bool // known_hosts 空的引导期告警只打一次
}

func (t *sshTunnel) Name() string { return pluginSSH }

func (t *sshTunnel) getOrCreate(ctx context.Context) (*ssh.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sshConn != nil {
		return t.sshConn, nil
	}
	c, err := t.dial(ctx)
	if err != nil {
		return nil, err
	}
	t.sshConn = c
	return c, nil
}

func (t *sshTunnel) reset(dead *ssh.Client) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sshConn == dead {
		_ = t.sshConn.Close()
		t.sshConn = nil
	}
}

// dial 建一条 SSH 连接：TCP 拨号吃 ctx 超时（与插件 TimeoutOrDefault 对齐），
// 随后 SSH 握手与认证（publickey 单一形态，无密码登录面）。
func (t *sshTunnel) dial(ctx context.Context) (*ssh.Client, error) {
	addr := t.opts.ServerName
	if addr == "" {
		return nil, errors.New("tunnel: ssh requires server_name (sshd host:port)")
	}
	keyPath := t.opts.KeyFile
	if keyPath == "" {
		return nil, errors.New("tunnel: ssh requires key_file (private key path)")
	}
	user := t.opts.AuthUser
	if user == "" {
		return nil, errors.New("tunnel: ssh requires auth_user")
	}
	if t.opts.ForwardAddr == "" {
		return nil, errors.New("tunnel: ssh requires forward_addr (ferry relay listen on landing host)")
	}
	pemBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("tunnel: read key file: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("tunnel: parse private key: %w", err)
	}
	hostKey := ssh.InsecureIgnoreHostKey() //nolint:gosec // 引导期口径：known_hosts 未配置时跳过，见 ssh_known_hosts 注记
	if t.opts.KnownHostsFile != "" {
		hk, err := knownHostsCallback(t.opts.KnownHostsFile)
		if err != nil {
			return nil, err
		}
		hostKey = hk
	} else if !t.warned {
		t.warned = true
		fmt.Fprintln(os.Stderr, "tunnel: ssh known_hosts not configured, host key check skipped (bootstrap only, configure ssh_known_hosts for production)")
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKey,
		Timeout:         t.opts.TimeoutOrDefault(),
	}
	d := &net.Dialer{Timeout: t.opts.TimeoutOrDefault()}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tunnel: ssh dial %s: %w", addr, err)
	}
	conn, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("tunnel: ssh handshake %s: %w", addr, err)
	}
	return ssh.NewClient(conn, chans, reqs), nil
}

func (t *sshTunnel) Dial(ctx context.Context, addr string) (net.Conn, error) {
	if t.opts.ForwardAddr == "" {
		return nil, errors.New("tunnel: ssh requires forward_addr (ferry relay listen on landing host)")
	}
	c, err := t.getOrCreate(ctx)
	if err != nil {
		return nil, err
	}
	// Client.Dial 直接返回 net.Conn（direct-tcpip channel 的流语义适配）。
	// 限制如实注记：x/crypto 的 channel 连接不支持 SetDeadline 系列
	// （返回 "deadline not supported"，PingPong 侧 `_ =` 忽略），心跳
	// 超时靠 3 周期无 pong 的应用层判定兜底，不靠 deadline 加速。
	// channel 开启无独立超时，死连接上立即回错，由重连腿兜底。
	conn, err := c.Dial("tcp", t.opts.ForwardAddr)
	if err != nil {
		// 复用连接可能已死（sshd 重启/网络抖动）：判定死掉重连一次再试，
		// 避免单次抖动把 relay 前端连接全带崩。
		t.reset(c)
		c2, err2 := t.getOrCreate(ctx)
		if err2 != nil {
			return nil, err
		}
		conn, err = c2.Dial("tcp", t.opts.ForwardAddr)
		if err != nil {
			return nil, fmt.Errorf("tunnel: ssh open channel %s: %w", t.opts.ForwardAddr, err)
		}
	}
	return conn, nil
}

func (t *sshTunnel) ServeHeartbeat(ctx context.Context, conn net.Conn, interval time.Duration) error {
	return PingPong(ctx, conn, interval)
}

// Close 释放复用连接（tunnelCache 持实例缓存，热重载换落地时统一关）。
func (t *sshTunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sshConn == nil {
		return nil
	}
	err := t.sshConn.Close()
	t.sshConn = nil
	return err
}

// knownHostsCallback 用 known_hosts 文件校验 host key（OpenSSH 同格式，
// hashed/明文条目均可）：knownhosts 库薄封装，与 x/crypto 同源。
func knownHostsCallback(path string) (ssh.HostKeyCallback, error) {
	return knownhosts.New(path)
}
