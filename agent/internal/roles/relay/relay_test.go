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

// registers a plaintext plugin for tests (Dial = bare TCP): proof that relay is decoupled from the transport;
// production uses tls-camo, and this test only verifies the forwarding plumbing.
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
	// landing echo
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

	// the relay's local listener
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = front.Close() // just occupies a port number; relay listens on its own
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
	time.Sleep(200 * time.Millisecond) // wait for the listener to be ready

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

// TestRelayHotRepoint covers hot-repointing via the landing override file (E-16b): after a SIGHUP re-read, new connections
// use the new landing, and a corrupt override file keeps the current one without interruption.
func TestRelayHotRepoint(t *testing.T) {
	// two landing echoes, each counting its own
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

	// waitConns dials repeatedly until the given landing has received want new connections (a hot reload takes effect asynchronously).
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

	// hot-repoint to b: after the re-read is applied new connections land on b (the reload is asynchronous, so poll until it takes effect).
	writeOV(b.ln.Addr().String())
	hup <- syscall.SIGHUP
	waitConns(b, 1)

	// a corrupt override file: keep the current one (b) without interruption.
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

// TestRelayRunFile covers the RunFile path: a missing override file is treated as an empty override and a corrupt one fails startup;
// it also listens normally and exits when the ctx is cancelled (covering the SIGHUP registration path as well).
func TestRelayRunFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := log.New(os.Stderr, "test ", 0)

	// a missing override file: startup is not blocked (treated as an empty override) and fails because listen is invalid
	if err := RunFile(ctx, config.RelaySpec{}, filepath.Join(t.TempDir(), "absent.json"), logger); err == nil {
		t.Fatal("empty spec must fail")
	}

	// a corrupt override file: startup fails early, surfacing inside run
	bad := filepath.Join(t.TempDir(), "ov.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RunFile(ctx, config.RelaySpec{Listen: "127.0.0.1:0", LandingAddr: "127.0.0.1:1",
		Tunnel: "plain-test"}, bad, logger); err == nil {
		t.Fatal("corrupt override must fail startup")
	}

	// the normal path: occupy a port number and let RunFile listen on its own
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	ok := filepath.Join(t.TempDir(), "ov.json")
	if err := os.WriteFile(ok, []byte(`{"landing_addr":"127.0.0.1:1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- RunFile(ctx, config.RelaySpec{
			Listen: addr, LandingAddr: "127.0.0.1:1", Tunnel: "plain-test",
		}, ok, logger)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, derr := net.Dial("tcp", addr); derr == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("RunFile did not listen")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunFile err = %v; want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunFile did not return after cancel")
	}
}
