package relay

import (
	"context"
	"log"
	"net"
	"os"
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
