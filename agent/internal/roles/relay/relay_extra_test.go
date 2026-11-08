package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/tunnel"
)

// TestRunDefaultsLoggerAndLandingRequired 缺省 logger 与缺落地指向的校验序。
func TestRunDefaultsLoggerAndLandingRequired(t *testing.T) {
	// logger 为 nil 用默认；listen 缺失先报
	err := run(context.Background(), config.RelaySpec{}, "", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "listen is required") {
		t.Fatalf("err = %v; want listen is required", err)
	}
	// 有 listen 无落地指向
	err = run(context.Background(),
		config.RelaySpec{Listen: "127.0.0.1:0"}, "", nil, nil,
		log.New(io.Discard, "", 0))
	if err == nil || !strings.Contains(err.Error(), "landing_addr is required") {
		t.Fatalf("err = %v; want landing_addr is required", err)
	}
}

// TestRunListenError 监听端口被占：启动失败尽早暴露。
func TestRunListenError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	err = run(context.Background(), config.RelaySpec{
		Listen:      occupied.Addr().String(),
		LandingAddr: "127.0.0.1:1",
		Tunnel:      "plain-test",
	}, "", nil, nil, log.New(io.Discard, "", 0))
	if err == nil {
		t.Fatal("occupied listen must fail")
	}
}

// TestMergeSpecOverrides 面板覆盖只吃落地相关字段的非空值。
func TestMergeSpecOverrides(t *testing.T) {
	base := config.RelaySpec{Listen: "127.0.0.1:9000", LandingAddr: "a:1"}
	ov := config.RelaySpec{LandingAddr: "b:2", Tunnel: "x", ServerName: "s", CAFile: "ca.pem"}
	got := mergeSpec(base, ov)
	if got.Listen != "127.0.0.1:9000" {
		t.Fatalf("listen must stay local: %+v", got)
	}
	if got.LandingAddr != "b:2" || got.Tunnel != "x" || got.ServerName != "s" || got.CAFile != "ca.pem" {
		t.Fatalf("overrides not applied: %+v", got)
	}
	// 空覆盖不动基底
	keep := mergeSpec(base, config.RelaySpec{})
	if keep != base {
		t.Fatalf("empty override must keep base: %+v", keep)
	}
}

// TestLoadOverrideMissingAndError 覆盖文件缺失是正常态（零值不报错）；
// 读失败（如路径是目录）要报错。
func TestLoadOverrideMissingAndError(t *testing.T) {
	got, err := loadOverride(filepath.Join(t.TempDir(), "no-such.json"))
	if err != nil || got != (config.RelaySpec{}) {
		t.Fatalf("missing override = %+v, %v; want zero value nil", got, err)
	}
	if _, err := loadOverride(t.TempDir()); err == nil {
		t.Fatal("directory path must error")
	}
}

// TestTunnelPluginOfDefault 传输插件缺省口径。
func TestTunnelPluginOfDefault(t *testing.T) {
	if got := tunnelPluginOf(config.RelaySpec{}); got != tunnel.DefaultPlugin {
		t.Fatalf("default plugin = %q; want %q", got, tunnel.DefaultPlugin)
	}
	if got := tunnelPluginOf(config.RelaySpec{Tunnel: "plain-test"}); got != "plain-test" {
		t.Fatalf("plugin = %q; want plain-test", got)
	}
}

// errTunnel Close 固定失败的假插件（closeAll 日志分支用）。
type errTunnel struct{}

func (errTunnel) Name() string { return "err-close" }
func (errTunnel) Dial(_ context.Context, _ string) (net.Conn, error) {
	return nil, errors.New("no dial")
}
func (errTunnel) ServeHeartbeat(ctx context.Context, _ net.Conn, _ time.Duration) error {
	<-ctx.Done()
	return nil
}
func (errTunnel) Close() error { return errors.New("close boom") }

// TestCloseAllLogsError 关缓存时单个插件关闭失败：记日志并继续清空。
func TestCloseAllLogsError(t *testing.T) {
	var buf bytes.Buffer
	c := newTunnelCache(log.New(&buf, "", 0))
	c.mu.Lock()
	c.m["broken"] = errTunnel{}
	c.mu.Unlock()
	c.closeAll()
	if len(c.m) != 0 {
		t.Fatal("cache must be cleared")
	}
	// errTunnel 的 Close 必须被调用并记日志
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(buf.String(), "relay close tunnel broken") {
		if time.Now().After(deadline) {
			t.Fatalf("close error not logged: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// acceptOnceListener 第一次 Accept 固定报错，其后阻塞到 Close（ctx 取消时
// run 内部会调 Close 解除阻塞）。
type acceptOnceListener struct {
	once   sync.Once
	closed chan struct{}
}

func newAcceptOnceListener() *acceptOnceListener {
	return &acceptOnceListener{closed: make(chan struct{})}
}

func (l *acceptOnceListener) Accept() (net.Conn, error) {
	fired := false
	l.once.Do(func() {
		fired = true
		close(l.closed)
	})
	if fired {
		return nil, errors.New("accept exploded")
	}
	<-l.closed // 之后一直阻塞，直到 Close（或第一次错误后立即放行）
	return nil, errors.New("listener closed")
}

func (l *acceptOnceListener) Close() error {
	l.once.Do(func() {}) // 未触发过错误时先消费 once，防 Close 后再误报
	return nil
}

func (l *acceptOnceListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

// TestRunAcceptErrorLogged 接受连接出错：记日志后继续，不中断服务循环。
func TestRunAcceptErrorLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- run(ctx, config.RelaySpec{
			Listen:      "127.0.0.1:0",
			LandingAddr: "127.0.0.1:1",
			Tunnel:      "plain-test",
		}, "", nil, newAcceptOnceListener(), logger)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(buf.String(), "relay accept") {
		if time.Now().After(deadline) {
			t.Fatalf("accept error not logged: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-runErr:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

// waitForLog 轮询日志缓冲出现子串。
func waitForLog(t *testing.T, buf *bytes.Buffer, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(buf.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("log %q never appeared; got %q", want, buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRunHupReloadBranches 覆盖热重载分支：覆盖清空落地保现行、无变化不
// 切换、换未知插件后新连接拨号报错不中断。落地初始来自覆盖文件（基底无
// landing），这样「清空落地」的覆盖才会合成空值触发保现行分支。
func TestRunHupReloadBranches(t *testing.T) {
	ovPath := filepath.Join(t.TempDir(), "override.json")
	hup := make(chan os.Signal, 4)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	// 启动覆盖先落盘（基底不带 landing，落地完全靠覆盖文件）
	if err := os.WriteFile(ovPath, []byte(`{"landing_addr":"10.1.1.1:443"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := run(ctx, config.RelaySpec{
			Listen: "127.0.0.1:0",
			Tunnel: "plain-test",
		}, ovPath, hup, ln, logger); err != nil {
			logger.Printf("run exited: %v", err)
		}
		runErr <- nil
	}()
	waitForLog(t, &buf, "relay listening on 127.0.0.1:0") // 启动装载完成后再动覆盖文件

	// 1) 清空落地：mergeSpec 回落到基底（基底 landing 为空）→ 保现行只记日志
	if err := os.WriteFile(ovPath, []byte(`{"landing_addr":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	hup <- syscall.SIGHUP
	waitForLog(t, &buf, "landing_addr empty, keep current")

	// 2) 与现行一致（回落基底后 landing 与现行同）：不切换无日志
	if err := os.WriteFile(ovPath, []byte(`{"landing_addr":"10.1.1.1:443","tunnel":"plain-test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	hup <- syscall.SIGHUP
	time.Sleep(150 * time.Millisecond) // 若误切换会打出 repointed，静默即正确
	if strings.Contains(buf.String(), "repointed") {
		t.Fatalf("unchanged override must not repoint: %q", buf.String())
	}

	// 3) 换未知插件：切成功，新连接拨号失败只断该连接
	if err := os.WriteFile(ovPath, []byte(`{"landing_addr":"10.1.1.2:443","tunnel":"no-such"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	hup <- syscall.SIGHUP
	waitForLog(t, &buf, "repointed -> 10.1.1.2:443")

	front, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	waitForLog(t, &buf, "relay tunnel no-such")
	_ = front.Close()

	cancel()
	select {
	case <-runErr:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}
