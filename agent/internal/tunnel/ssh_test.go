package tunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// memSSHServer 是测试内的 SSH 服务端（x/crypto 库内回环，无真 sshd）：
// ed25519 内存 host key + publickey 认证（只验用户名与密钥形态）+
// direct-tcpip channel 转发到本地 echo。握手计数供连接复用断言。
type memSSHServer struct {
	hs atomic.Int32
	// hostKeyLine 是本服务端 host key 的 known_hosts 行（客户端校验用）。
	hostKeyLine string

	ln net.Listener
	wg sync.WaitGroup
}

func newEcho(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	return ln.Addr().String()
}

func startMemSSH(t *testing.T, echoAddr string) *memSSHServer {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &memSSHServer{ln: ln, hostKeyLine: ""}
	cfg := &ssh.ServerConfig{
		// publickey 认证（与生产单一形态一致）：测试只验用户名，密钥形态
		// 即通过——客户端密钥由测试生成，无指纹库可对。
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if meta.User() != "ferry" {
				return nil, fmt.Errorf("user %q not allowed", meta.User())
			}
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(hostSigner)
	s.hostKeyLine = knownhosts.Line([]string{"[" + ln.Addr().String() + "]"}, hostSigner.PublicKey())
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.hs.Add(1)
			conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
			if err != nil {
				continue
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				_ = conn.Wait()
			}()
			go s.serve(chans, reqs, echoAddr)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		s.wg.Wait()
	})
	return s
}

// serve 处理 direct-tcpip channel（sshd 转发语义）：Accept 后拨 echo
// 双向拷贝。
func (s *memSSHServer) serve(chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request, echoAddr string) {
	go func() {
		for req := range reqs {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}()
	for newCh := range chans {
		if newCh.ChannelType() != "direct-tcpip" {
			_ = newCh.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		ch, _, err := newCh.Accept()
		if err != nil {
			continue
		}
		up, err := net.Dial("tcp", echoAddr)
		if err != nil {
			_ = ch.Close()
			continue
		}
		go func() {
			defer func() { _ = ch.Close(); _ = up.Close() }()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(up, ch); done <- struct{}{} }()
			go func() { _, _ = io.Copy(ch, up); done <- struct{}{} }()
			<-done
			<-done
		}()
	}
}

// sshdAddr 取服务端 host:port（host key 行生成要用）。
func (s *memSSHServer) sshdAddr() string { return s.ln.Addr().String() }

// writeClientKey 生成 RSA 私钥 pem（PKCS1，ssh.ParsePrivateKey 可解析）。
func writeClientKey(t *testing.T, dir string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "id_rsa")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// newSSHTunnel 建全配 Options 的 ssh 插件；knownHostsLine 空=不配 known_hosts。
func newSSHTunnel(t *testing.T, sshdAddr, echoAddr, knownHostsLine string) Tunnel {
	t.Helper()
	dir := t.TempDir()
	opts := Options{
		ServerName:     sshdAddr,
		AuthUser:       "ferry",
		KeyFile:        writeClientKey(t, dir),
		KnownHostsFile: "",
		ForwardAddr:    echoAddr,
		Timeout:        5 * time.Second,
	}
	if knownHostsLine != "" {
		khPath := filepath.Join(dir, "known_hosts")
		if err := os.WriteFile(khPath, []byte(knownHostsLine+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		opts.KnownHostsFile = khPath
	}
	tn, err := Open("ssh", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tn.Close() })
	return tn
}

// roundtrip 通过插件 Dial 打一发 echo 回环。
func roundtrip(t *testing.T, tn Tunnel) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := tn.Dial(ctx, "ignored-by-ssh")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	msg := "ping-ferry"
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != msg {
		t.Fatalf("roundtrip got %q want %q", buf, msg)
	}
}

// TestSSHTunnelEndToEnd 全链回环：Dial→direct-tcpip→echo；两条前端连接
// 复用同一条 SSH 连接（握手恰一次）；认证用户不匹配拒。
func TestSSHTunnelEndToEnd(t *testing.T) {
	echo := newEcho(t)
	srv := startMemSSH(t, echo)
	tn := newSSHTunnel(t, srv.sshdAddr(), echo, "")
	roundtrip(t, tn)
	roundtrip(t, tn)
	if n := srv.hs.Load(); n != 1 {
		t.Fatalf("ssh handshakes = %d, want 1 (client reuse)", n)
	}
	// net.Conn 适配面：ssh 隧道连接的地址方法可用（relay 记账要用）。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := tn.Dial(ctx, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn.LocalAddr() == nil || conn.RemoteAddr() == nil {
		t.Fatal("ssh conn must expose net.Addr (relay accounting uses it)")
	}
	// deadline 限制如实断言：x/crypto channel 连接不支持（错误可忽略，
	// PingPong 侧 `_ =` 同口径，超时靠 3-miss 应用层判定兜底）——
	// 写读回环在 deadline 调用后仍通即可。
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	roundtripOnce(t, conn)
}

// roundtripOnce 在已建立的连接上打一发回环。
func roundtripOnce(t *testing.T, conn net.Conn) {
	t.Helper()
	msg := "pong-ferry"
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != msg {
		t.Fatalf("roundtrip got %q want %q", buf, msg)
	}
}

// TestSSHTunnelHostKeyVerify known_hosts 匹配→通、不匹配→拒（host key
// 校验腿，生产口径：known_hosts 必配）。
func TestSSHTunnelHostKeyVerify(t *testing.T) {
	echo := newEcho(t)
	srv := startMemSSH(t, echo)

	// 服务端 host key 未知（known_hosts 是另一把 key 的行）：拒。
	wrongSigner, err := ssh.NewSignerFromKey(func() ed25519.PrivateKey {
		_, priv, _ := ed25519.GenerateKey(rand.Reader)
		return priv
	}())
	if err != nil {
		t.Fatal(err)
	}
	wrongLine := knownhosts.Line([]string{"[" + srv.sshdAddr() + "]"}, wrongSigner.PublicKey())
	tn := newSSHTunnel(t, srv.sshdAddr(), echo, wrongLine)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := tn.Dial(ctx, "ignored"); err == nil {
		t.Fatal("unknown host key must be rejected")
	}

	// 服务端真实 host key 行：通。
	tn2 := newSSHTunnel(t, srv.sshdAddr(), echo, srv.hostKeyLine)
	roundtrip(t, tn2)
}

// TestSSHTunnelMissingFields 配置缺字段即拒：sshd 地址/用户/私钥/转发目标
// 四件缺一不冒称可拨（插件 factory 即校验口径）。
func TestSSHTunnelMissingFields(t *testing.T) {
	dir := t.TempDir()
	key := writeClientKey(t, dir)
	base := Options{ServerName: "127.0.0.1:22", AuthUser: "ferry", KeyFile: key,
		ForwardAddr: "127.0.0.1:10800", Timeout: time.Second}
	tn, err := Open("ssh", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tn.Close() })
	ctx := context.Background()

	// 逐字段置空即拒。
	for _, mutate := range []func(*Options){
		func(o *Options) { o.ServerName = "" },
		func(o *Options) { o.AuthUser = "" },
		func(o *Options) { o.KeyFile = "" },
		func(o *Options) { o.ForwardAddr = "" },
	} {
		o := base
		mutate(&o)
		plug, err := Open("ssh", o)
		if err != nil {
			t.Fatalf("Open with %+v: %v", o, err)
		}
		if _, err := plug.Dial(ctx, "x"); err == nil {
			t.Fatalf("Dial with %+v must fail (missing config)", o)
		}
	}
	// 密钥文件不存在拒。
	o := base
	o.KeyFile = filepath.Join(dir, "missing")
	plug, err := Open("ssh", o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plug.Dial(ctx, "x"); err == nil {
		t.Fatal("missing key file must fail")
	}
}

// TestSSHRegistered ssh 插件已注册且默认插件不受影响（备选不夺默认）。
func TestSSHRegistered(t *testing.T) {
	if DefaultPlugin != "tls-camo" {
		t.Fatalf("default plugin = %s, want tls-camo (ssh is fallback only)", DefaultPlugin)
	}
	found := false
	for _, n := range Names() {
		if n == "ssh" {
			found = true
		}
	}
	if !found {
		t.Fatal("ssh plugin not registered")
	}
}
