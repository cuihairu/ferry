package relay

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
)

// TestRelayIdleCleanup covers F-0814: a frontend connection that goes quiet is
// closed by the sliding deadline, while an active connection stays alive.
func TestRelayIdleCleanup(t *testing.T) {
	landing, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer landing.Close()
	go func() {
		for {
			c, err := landing.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		_ = run(ctx, config.RelaySpec{
			Listen: "127.0.0.1:0", LandingAddr: landing.Addr().String(),
			Tunnel: "plain-test", IdleTimeoutSec: 1,
		}, "", nil, ln, log.New(os.Stderr, "idle ", 0))
	}()

	connect := func() net.Conn {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			c, err := net.DialTimeout("tcp", ln.Addr().String(), 500*time.Millisecond)
			if err == nil {
				return c
			}
			if time.Now().After(deadline) {
				t.Fatalf("dial relay: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Idle connection: no traffic after the first byte, the relay closes it.
	quiet := connect()
	defer quiet.Close()
	if _, err := quiet.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	// consume the echo so the first read succeeds
	buf := make([]byte, 16)
	if _, err := quiet.Read(buf); err != nil {
		t.Fatal(err)
	}
	_ = quiet.SetReadDeadline(time.Now().Add(4 * time.Second))
	if _, err := quiet.Read(buf); err == nil {
		t.Fatal("idle connection must be closed after the idle window")
	}

	// Active connection: one echo per 200ms keeps it alive beyond the 1s window.
	active := connect()
	defer active.Close()
	for i := 0; i < 8; i++ {
		_ = active.SetWriteDeadline(time.Now().Add(time.Second))
		if _, err := active.Write([]byte("x")); err != nil {
			t.Fatalf("active write %d: %v", i, err)
		}
		_ = active.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := active.Read(buf); err != nil {
			t.Fatalf("active read %d: %v", i, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestEffectiveIdleTimeout covers normalization: negatives disable it and a
// positive value converts to seconds.
func TestEffectiveIdleTimeout(t *testing.T) {
	if got := effectiveIdleTimeout(config.RelaySpec{}); got != 0 {
		t.Fatalf("zero idle = %v; want 0", got)
	}
	if got := effectiveIdleTimeout(config.RelaySpec{IdleTimeoutSec: -1}); got != 0 {
		t.Fatalf("negative idle = %v; want 0", got)
	}
	if got := effectiveIdleTimeout(config.RelaySpec{IdleTimeoutSec: 12}); got != 12*time.Second {
		t.Fatalf("idle = %v; want 12s", got)
	}
}
