package tunnel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

// ---- ssh 插件：校验序与错误面 ----

// sshOpts 组一份合法 Options，再按需逐项破坏。
func sshOpts(t *testing.T, mutate func(*Options)) Options {
	t.Helper()
	dir := t.TempDir()
	opts := Options{
		ServerName:  "127.0.0.1:22",
		AuthUser:    "ferry",
		KeyFile:     writeClientKey(t, dir),
		ForwardAddr: "127.0.0.1:9000",
		Timeout:     time.Second,
	}
	if mutate != nil {
		mutate(&opts)
	}
	return opts
}

// TestSSHDialValidationErrors dial 前置校验与凭证加载错误面：缺 auth_user、
// 密钥文件缺失/坏内容、known_hosts 文件打不开、TCP 拨号失败。
func TestSSHDialValidationErrors(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"auth_user missing", sshOpts(t, func(o *Options) { o.AuthUser = "" }), "auth_user"},
		{"key file missing", sshOpts(t, func(o *Options) { o.KeyFile = filepath.Join(t.TempDir(), "no-key") }), "read key file"},
		{"known_hosts missing", sshOpts(t, func(o *Options) { o.KnownHostsFile = filepath.Join(t.TempDir(), "no-kh") }), "no such file"},
		{"tcp dial fail", sshOpts(t, func(o *Options) { o.ServerName = "127.0.0.1:1" }), "ssh dial"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := (&sshTunnel{opts: c.opts}).dial(ctx)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v; want contains %q", err, c.want)
			}
		})
	}

	// 坏密钥内容：文件在但解析不了
	badKey := filepath.Join(t.TempDir(), "bad-key")
	if err := os.WriteFile(badKey, []byte("not a pem at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (&sshTunnel{opts: sshOpts(t, func(o *Options) { o.KeyFile = badKey })}).dial(ctx)
	if err == nil || !strings.Contains(err.Error(), "parse private key") {
		t.Fatalf("err = %v; want parse private key error", err)
	}
}

// TestSSHWarnsOnceWithoutKnownHosts known_hosts 未配（引导期口径）：拨号
// 前打一次告警（warned 置位），后续不再置位。
func TestSSHWarnsOnceWithoutKnownHosts(t *testing.T) {
	tn := &sshTunnel{opts: sshOpts(t, func(o *Options) { o.ServerName = "127.0.0.1:1" })}
	ctx := context.Background()
	if _, err := tn.dial(ctx); err == nil {
		t.Fatal("dial must fail on refused addr")
	}
	if !tn.warned {
		t.Fatal("bootstrap warning must be recorded on first dial")
	}
	if _, err := tn.dial(ctx); err == nil {
		t.Fatal("second dial must fail as well")
	}
}

// rejectingSSHD 只握手、拒绝一切 direct-tcpip 开流请求的 sshd 替身：专门
// 演练 ssh 插件的「开流失败 → 判死重连」恢复腿。
type rejectingSSHD struct {
	ln net.Listener
	wg sync.WaitGroup
}

func startRejectingSSHD(t *testing.T) *rejectingSSHD {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)
	s := &rejectingSSHD{ln: ln}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
			if err != nil {
				continue
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				_ = conn.Wait()
			}()
			go func(chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) {
				for req := range reqs {
					if req.WantReply {
						_ = req.Reply(false, nil)
					}
				}
			}(chans, reqs)
			go func(chans <-chan ssh.NewChannel) {
				for newCh := range chans {
					_ = newCh.Reject(ssh.Prohibited, "no forwarding in test")
				}
			}(chans)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		s.wg.Wait()
	})
	return s
}

// TestSSHDialRetryOnChannelReject 开流被拒：判死缓存连接并重连再试一次；
// 重连后仍被拒 → 报 open channel。sshd 停收新连接后：复用连接仍能开流
// 失败 → 重连腿自身失败 → 保留首个错误。
func TestSSHDialRetryOnChannelReject(t *testing.T) {
	s := startRejectingSSHD(t)
	tn := newSSHTunnel(t, s.ln.Addr().String(), "unused-by-ssh", "").(*sshTunnel)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := tn.Dial(ctx, "ignored")
	if err == nil || !strings.Contains(err.Error(), "open channel") {
		t.Fatalf("err = %v; want open channel error after retry", err)
	}

	// 缓存里应有一条重连后的连接（重试腿建过新连接）
	tn.mu.Lock()
	cached := tn.sshConn
	tn.mu.Unlock()
	if cached == nil {
		t.Fatal("retry leg must leave a cached connection")
	}

	// 停收新连接（已有连接仍活着、仍拒绝开流）：重连腿失败，保留首个错误
	// （原样上抛，不带 open channel 包装）
	if err := s.ln.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = tn.Dial(ctx, "ignored")
	if err == nil || !strings.Contains(err.Error(), "missing port in address") {
		t.Fatalf("err = %v; want preserved first dial error", err)
	}
	tn.mu.Lock()
	gone := tn.sshConn == nil
	tn.mu.Unlock()
	if !gone {
		t.Fatal("dead cache must be cleared after failed redial")
	}
}

// ---- quic sidecar 插件：地址缺省与 SNI 回落、无应答 ----

// TestQUICSidecarAddrDefault 环境变量空白视同未配置：回落缺省桥地址。
func TestQUICSidecarAddrDefault(t *testing.T) {
	t.Setenv(SidecarEnv, "   ")
	if got := SidecarAddr(); got != DefaultSidecarAddr {
		t.Fatalf("SidecarAddr = %q; want %q", got, DefaultSidecarAddr)
	}
}

// TestQUICSidecarSNIFallbackFromAddr ServerName 未配：SNI 取目标 host；
// 目标无端口时整体即 host。
func TestQUICSidecarSNIFallbackFromAddr(t *testing.T) {
	var gotReq string
	sidecar := startFakeSidecar(t, func(req string) string {
		gotReq = req
		return "OK\n"
	})
	t.Setenv(SidecarEnv, sidecar)
	tn, err := Open(pluginQUIC, Options{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := tn.Dial(context.Background(), "example.com") // 无端口目标
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if !strings.HasPrefix(gotReq, "CONNECT example.com sni=example.com") {
		t.Fatalf("request = %q; want sni fallback to whole addr", gotReq)
	}
}

// TestQUICSidecarNoReply sidecar 收到请求后不回话即断：报 read connect
// reply 而不是悬挂。
func TestQUICSidecarNoReply(t *testing.T) {
	silent := startFakeSidecar(t, func(string) string { return "" })
	t.Setenv(SidecarEnv, silent)
	tn, _ := Open(pluginQUIC, Options{Timeout: 3 * time.Second})
	_, err := tn.Dial(context.Background(), "landing.example.com:443")
	if err == nil || !strings.Contains(err.Error(), "read connect reply") {
		t.Fatalf("err = %v; want read connect reply error", err)
	}
}

// ---- tls-camo / ws-tls 插件：ServerName 回落与 CA 文件错误 ----

// TestTLSCamoServerNameFallback ServerName 未配时取目标 host（无端口目标
// 取整体）；失败面断言拨号错误而非空指针。
func TestTLSCamoServerNameFallback(t *testing.T) {
	tn := &tlsCamo{}
	if _, err := tn.Dial(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("dial without port must fail")
	}
}

// TestTLSCamoCAFileErrors CA 文件缺失与无证书内容两种错误。
func TestTLSCamoCAFileErrors(t *testing.T) {
	tn := &tlsCamo{opts: Options{CAFile: filepath.Join(t.TempDir(), "no-ca.pem")}}
	_, err := tn.Dial(context.Background(), "127.0.0.1:443")
	if err == nil || !strings.Contains(err.Error(), "read CA file") {
		t.Fatalf("err = %v; want read CA file error", err)
	}
	junk := filepath.Join(t.TempDir(), "junk.pem")
	if err := os.WriteFile(junk, []byte("garbage, not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	tn = &tlsCamo{opts: Options{CAFile: junk}}
	_, err = tn.Dial(context.Background(), "127.0.0.1:443")
	if err == nil || !strings.Contains(err.Error(), "no certificates in") {
		t.Fatalf("err = %v; want no-certificates error", err)
	}
}

// TestWSTLSServerNameFallback ServerName 未配：取目标 host（无端口取整体），
// 拨号失败原样上抛。
func TestWSTLSServerNameFallback(t *testing.T) {
	tn := &wsTls{}
	if _, err := tn.Dial(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("dial without port must fail")
	}
}

// TestWSTLSCAFileErrors CA 文件缺失与无证书内容两种错误。
func TestWSTLSCAFileErrors(t *testing.T) {
	tn := &wsTls{opts: Options{CAFile: filepath.Join(t.TempDir(), "no-ca.pem")}}
	_, err := tn.Dial(context.Background(), "127.0.0.1:443")
	if err == nil || !strings.Contains(err.Error(), "read CA file") {
		t.Fatalf("err = %v; want read CA file error", err)
	}
	junk := filepath.Join(t.TempDir(), "junk.pem")
	if err := os.WriteFile(junk, []byte("garbage, not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	tn = &wsTls{opts: Options{CAFile: junk}}
	_, err = tn.Dial(context.Background(), "127.0.0.1:443")
	if err == nil || !strings.Contains(err.Error(), "no certificates in") {
		t.Fatalf("err = %v; want no-certificates error", err)
	}
}

// startTextThenCloseWS 起一个发 TextMessage 后即发关闭帧的 ws 服务替身：
// 演练 wsConn 读侧跳过非二进制消息。
func startTextThenCloseWS(t *testing.T) string {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		if err := c.WriteMessage(websocket.TextMessage, []byte("control junk")); err != nil {
			return
		}
		_ = c.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"),
			time.Now().Add(time.Second))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "https://")
}

// dialRawWSConn 白盒拿 *wsConn（不做回环，只测读/写适配层的边界分支）。
func dialRawWSConn(t *testing.T, addr string) *wsConn {
	t.Helper()
	d := &websocket.Dialer{
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // 测试自签证书
		HandshakeTimeout: 3 * time.Second,
	}
	ws, _, err := d.Dial("wss://"+addr+"/ferry", nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	return &wsConn{ws: ws}
}

// TestWSConnReadSkipsNonBinary 非二进制消息不交给上层：跳过后遇关闭帧
// 上抛读错误（而不是把 Text 内容当业务字节）。
func TestWSConnReadSkipsNonBinary(t *testing.T) {
	c := dialRawWSConn(t, startTextThenCloseWS(t))
	defer c.Close()
	buf := make([]byte, 64)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err := c.Read(buf)
	if err == nil {
		t.Fatalf("want read error after text skip + close, got data %q", buf)
	}
}

// TestWSConnWriteAfterClose 连接已关：读与写的适配层都要报错而不是
// 假成功。
func TestWSConnWriteAfterClose(t *testing.T) {
	addr, _ := startWSServer(t)
	c := dialRawWSConn(t, addr)
	_ = c.ws.Close()
	if _, err := c.Write([]byte("x")); err == nil {
		t.Fatal("write on closed ws must fail")
	}
	if _, err := c.Read(make([]byte, 8)); err == nil {
		t.Fatal("read on closed ws must fail")
	}
}

// ---- 心跳 PingPong：缺省周期与错误/恢复分支 ----

// scriptConn 按剧本供读（字节流）的假连接；werr 非空时写固定失败。
type scriptConn struct {
	r    io.Reader
	werr error
}

func (c *scriptConn) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *scriptConn) Write(p []byte) (int, error) {
	if c.werr != nil {
		return 0, c.werr
	}
	return len(p), nil
}
func (c *scriptConn) Close() error                     { return nil }
func (c *scriptConn) LocalAddr() net.Addr              { return nil }
func (c *scriptConn) RemoteAddr() net.Addr             { return nil }
func (c *scriptConn) SetDeadline(time.Time) error      { return nil }
func (c *scriptConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptConn) SetWriteDeadline(time.Time) error { return nil }

// frame 拼一条 9 字节心跳帧。
func frame(kind byte, nonce uint64) []byte {
	b := make([]byte, frameLen)
	b[0] = kind
	binary.BigEndian.PutUint64(b[1:], nonce)
	return b
}

// blockedTail 字节读完后续一段永不就绪的读（替代 EOF，避免读协程提前报错）。
func blockedTail(t *testing.T) io.Reader {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a
}

// TestPingPongDefaultInterval interval<=0 用缺省 30s，ctx 取消干净返回。
func TestPingPongDefaultInterval(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立刻取消：返回 nil 即到位
	if err := PingPong(ctx, a, 0); err != nil {
		t.Fatalf("PingPong = %v; want nil on cancel", err)
	}
	_ = a.Close()
}

// TestPingPongPongWriteFails 收到 ping 回 pong 时写失败：错误上抛。
func TestPingPongPongWriteFails(t *testing.T) {
	c := &scriptConn{
		r:    io.MultiReader(bytes.NewReader(frame(framePing, 7)), blockedTail(t)),
		werr: errors.New("broken pipe"),
	}
	err := PingPong(context.Background(), c, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "broken pipe") {
		t.Fatalf("err = %v; want broken pipe", err)
	}
}

// TestPingPongPingWriteFails 周期到点发 ping 写失败：错误上抛。
func TestPingPongPingWriteFails(t *testing.T) {
	c := &scriptConn{r: blockedTail(t), werr: errors.New("gone")}
	err := PingPong(context.Background(), c, 15*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("err = %v; want gone", err)
	}
}

// TestPingPongUnknownFrame 未知帧类型：报错而不是误当业务帧吞掉。
func TestPingPongUnknownFrame(t *testing.T) {
	c := &scriptConn{r: io.MultiReader(bytes.NewReader(frame(0x7F, 1)), blockedTail(t))}
	err := PingPong(context.Background(), c, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "unknown heartbeat frame") {
		t.Fatalf("err = %v; want unknown frame error", err)
	}
}

// TestPingPongPongFloodBufferFull pong 洪泛冲爆缓冲（16）：多余的经
// default 丢弃、读协程不阻塞；随后再无 pong → 三周期判死收场。
func TestPingPongPongFloodBufferFull(t *testing.T) {
	var frames []byte
	for i := 0; i < 40; i++ { // 远超 pongs 缓冲 16
		frames = append(frames, frame(framePong, uint64(i))...)
	}
	c := &scriptConn{r: io.MultiReader(bytes.NewReader(frames), blockedTail(t))}
	done := make(chan error, 1)
	go func() { done <- PingPong(context.Background(), c, 10*time.Millisecond) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrHeartbeatLost) {
			t.Fatalf("err = %v; want ErrHeartbeatLost after flood", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PingPong did not conclude after flood")
	}
}

// TestPingPongOuterPongReset 周期之间收到 pong：外层 select 消费并复位
// miss 计数（对端主动回声场景）。
func TestPingPongOuterPongReset(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = c.Write(frame(framePong, 1)) // 主动 pong：早于首个 tick
		buf := make([]byte, 64)
		for { // 吃掉后续 ping 保持对端写侧畅通
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}()
	a, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- PingPong(ctx, a, 100*time.Millisecond) }()
	time.Sleep(50 * time.Millisecond) // pong 落在两个 tick 之间
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PingPong = %v; want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PingPong did not return on cancel")
	}
}
