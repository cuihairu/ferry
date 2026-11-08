package quictunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// TestParseConnectLineEdgeBranches 补解析边界：空目标地址报错、无端口目标
// 以整体作 SNI、空 sni= 值忽略后回落 host。
func TestParseConnectLineEdgeBranches(t *testing.T) {
	if _, _, err := ParseConnectLine("CONNECT \n"); err == nil || !strings.Contains(err.Error(), "missing target address") {
		t.Fatalf("err = %v; want missing target address", err)
	}
	addr, sni, err := ParseConnectLine("CONNECT example.com\n")
	if err != nil || addr != "example.com" || sni != "example.com" {
		t.Fatalf("no-port target = %q/%q err %v; want host as sni", addr, sni, err)
	}
	addr, sni, err = ParseConnectLine("CONNECT a.example.com:8443 sni= sni=real.example.com\n")
	if err != nil || sni != "real.example.com" {
		t.Fatalf("empty sni= ignored: %q err %v; want real.example.com", sni, err)
	}
}

// TestReadConnectLineError 对端不发请求行就断开：读失败原样上抛。
func TestReadConnectLineError(t *testing.T) {
	a, b := net.Pipe()
	_ = b.Close()
	if _, _, err := readConnectLine(a, time.Second); err == nil {
		t.Fatal("closed peer must fail readConnectLine")
	}
	_ = a.Close()
}

// TestServeAcceptErrorReturns 监听器失效（ctx 仍存活）时 Serve 返回该错误，
// 而不是吞掉。
func TestServeAcceptErrorReturns(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close() // 先关：Accept 立即报错且 ctx 未取消

	c := NewClient(&tls.Config{})
	err = c.Serve(context.Background(), ln, nil)
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want listener error while ctx alive", err)
	}
}

// TestServeOneReadConnectError 本地连接不发 CONNECT 即断：serveOne 报
// read connect 并被 Serve 记日志（单条失败不断服）。
func TestServeOneReadConnectError(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	_, pool := selfSignedTLSPair(t)
	c := NewClient(&tls.Config{RootCAs: pool})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- c.Serve(ctx, ln, logger) }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close() // 不发请求行直接断

	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(buf.String(), "read connect") {
		if time.Now().After(deadline) {
			t.Fatalf("read connect error not logged: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-serveErr:
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

// TestOpenStreamStaleCacheRecovers 缓存连接陈旧（本地模拟对端已回收）：
// 开流失败要清缓存、重拨并成功打通，新连接重新入缓存。
func TestOpenStreamStaleCacheRecovers(t *testing.T) {
	cert, pool := selfSignedTLSPair(t)
	target := startEchoTarget(t)
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr := &quic.Transport{Conn: udp}
	defer tr.Close()
	qln, err := tr.Listen(&tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{ALPNIdent},
	}, DefaultQUICConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ServeServer(ctx, qln, target, nil) }()

	c := NewClient(&tls.Config{RootCAs: pool})
	addr := udp.LocalAddr().String()
	key := addr + "|127.0.0.1"
	stale, err := c.dial(ctx, addr, "127.0.0.1")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = stale.CloseWithError(0, "simulated stale") // 连接本地报废，缓存仍指向它

	st, err := c.openStream(ctx, addr, "127.0.0.1") // 陈旧开流 → drop → 重拨
	if err != nil {
		t.Fatalf("openStream after stale: %v", err)
	}
	fresh := c.get(key)
	if fresh == nil || fresh == stale {
		t.Fatal("cache must hold a new conn after stale drop")
	}

	// 打通验证：重拨后的连接经服务端转发到 echo 落地（不关写侧，读回声）
	if _, err := st.Write([]byte("roundtrip")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = st.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, len("roundtrip"))
	if _, err := io.ReadFull(st, got); err != nil || string(got) != "roundtrip" {
		t.Fatalf("echo = %q err %v; want roundtrip", got, err)
	}

	// 复用缓存路径：第二次开流直接走已缓存的健康连接，不重拨
	again, err := c.openStream(ctx, addr, "127.0.0.1")
	if err != nil {
		t.Fatalf("cached openStream: %v", err)
	}
	if c.get(key) != fresh {
		t.Fatal("cached conn must be reused, not redialed")
	}
	_ = again.Close()
}

// TestServeServerAcceptErrorReturns QUIC 监听失效（ctx 仍存活）时
// ServeServer 返回该错误。
func TestServeServerAcceptErrorReturns(t *testing.T) {
	cert, _ := selfSignedTLSPair(t)
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr := &quic.Transport{Conn: udp}
	qln, err := tr.Listen(&tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{ALPNIdent},
	}, DefaultQUICConfig())
	if err != nil {
		t.Fatal(err)
	}
	_ = tr.Close() // 先关传输：Accept 立即报错且 ctx 未取消

	err = ServeServer(context.Background(), qln, "127.0.0.1:1", nil)
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v; want listener error while ctx alive", err)
	}
}

// TestForwardStreamDialFailureLogs 流已建立但落地目标不可达：关流并记
// dial target 日志（不影响其他流）。
func TestForwardStreamDialFailureLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	cert, pool := selfSignedTLSPair(t)
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr := &quic.Transport{Conn: udp}
	defer tr.Close()
	qln, err := tr.Listen(&tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{ALPNIdent},
	}, DefaultQUICConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // 手工服务侧：接一条流就转发给不可达端口
		qc, err := qln.Accept(ctx)
		if err != nil {
			return
		}
		st, err := qc.AcceptStream(ctx)
		if err != nil {
			return
		}
		forwardStream(st, "127.0.0.1:1", logger)
	}()

	c := NewClient(&tls.Config{RootCAs: pool})
	qc, err := c.dial(ctx, udp.LocalAddr().String(), "127.0.0.1")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = qc.CloseWithError(0, "done") }()
	st, err := qc.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if _, err := st.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(buf.String(), "dial target 127.0.0.1:1") {
		if time.Now().After(deadline) {
			t.Fatalf("dial failure not logged: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
