package relay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/tunnel"
)

// 注册测试用明文插件（Dial=裸 TCP）：relay 与传输解耦的证明，
// 生产走 tls-camo，本测试只验证转发 plumbing。
func init() {
	_ = tunnel.Register("plain-test", func(o tunnel.Options) (tunnel.Tunnel, error) {
		return &plain{}, nil
	})
}

type plain struct{}

func (p *plain) Name() string { return "plain-test" }
func (p *plain) Dial(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}
func (p *plain) ServeHeartbeat(ctx context.Context, conn net.Conn, d time.Duration) error {
	<-ctx.Done()
	return nil
}
func (p *plain) Close() error { return nil }

func TestRelayForwards(t *testing.T) {
	// 落地回显
	back, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	go func() {
		for {
			c, err := back.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if _, err := c.Write(buf[:n]); err != nil {
						return
					}
				}
			}(c)
		}
	}()

	// relay 本机监听
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = front.Close() // 仅占个端口号，relay 自己监听
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.New(os.Stderr, "test ", 0)
	go func() {
		_ = Run(ctx, config.RelaySpec{
			Listen:      front.Addr().String(),
			LandingAddr: back.Addr().String(),
			Tunnel:      "plain-test",
		}, logger)
	}()
	time.Sleep(200 * time.Millisecond) // 等监听就绪

	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err = net.Dial("tcp", front.Addr().String())
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial relay: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("hello-relay")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "hello-relay" {
		t.Fatalf("echo = %q", buf[:n])
	}
}

func TestRelayValidation(t *testing.T) {
	logger := log.New(os.Stderr, "test ", 0)
	if err := Run(context.Background(), config.RelaySpec{}, logger); err == nil {
		t.Fatal("empty spec must fail")
	}
	if err := Run(context.Background(), config.RelaySpec{
		Listen: "127.0.0.1:0", LandingAddr: "127.0.0.1:1", Tunnel: "no-such",
	}, logger); err == nil {
		t.Fatal("unknown tunnel must fail")
	}
}

// TestRelayHotRepoint 覆盖落地覆盖文件热重指（E-16b）：SIGHUP 重读后新连接
// 走新落地，坏覆盖文件保现行不中断。
func TestRelayHotRepoint(t *testing.T) {
	// 两个落地回显，各自计数
	type landing struct {
		ln    net.Listener
		mu    sync.Mutex
		conns int
	}
	newLanding := func() *landing {
		l := &landing{}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		l.ln = ln
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				l.mu.Lock()
				l.conns++
				l.mu.Unlock()
				go func(c net.Conn) {
					defer c.Close()
					buf := make([]byte, 1024)
					for {
						n, err := c.Read(buf)
						if err != nil {
							return
						}
						if _, err := c.Write(buf[:n]); err != nil {
							return
						}
					}
				}(c)
			}
		}()
		return l
	}
	a, b := newLanding(), newLanding()
	defer a.ln.Close()
	defer b.ln.Close()

	ovPath := filepath.Join(t.TempDir(), "relay-override.json")
	writeOV := func(addr string) {
		t.Helper()
		body := fmt.Sprintf(`{"landing_addr":%q,"tunnel":"plain-test"}`, addr)
		if err := os.WriteFile(ovPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeOV(a.ln.Addr().String())

	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()
	hup := make(chan os.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.New(os.Stderr, "test-hot ", 0)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, config.RelaySpec{Listen: front.Addr().String()}, ovPath, hup, front, logger)
	}()

	dialEcho := func() {
		t.Helper()
		var conn net.Conn
		deadline := time.Now().Add(5 * time.Second)
		for {
			conn, err = net.Dial("tcp", front.Addr().String())
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("dial relay: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 16)
		if _, err := conn.Read(buf); err != nil {
			t.Fatalf("echo: %v", err)
		}
	}

	// waitConns 反复拨号直到指定落地收到 want 条新连接（热重载异步生效）。
	waitConns := func(l *landing, want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			l.mu.Lock()
			got := l.conns
			l.mu.Unlock()
			if got >= want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("landing conns = %d, want %d", got, want)
			}
			dialEcho()
			time.Sleep(50 * time.Millisecond)
		}
	}

	dialEcho()
	waitConns(a, 1)

	// 热重指到 b：重读应用后新连接落 b（重载是异步的，轮询到生效为止）。
	writeOV(b.ln.Addr().String())
	hup <- syscall.SIGHUP
	waitConns(b, 1)

	// 坏覆盖文件：保现行（b），不中断。
	if err := os.WriteFile(ovPath, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	hup <- syscall.SIGHUP
	dialEcho()
	waitConns(b, 2)

	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("run exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not exit on cancel")
	}
}
