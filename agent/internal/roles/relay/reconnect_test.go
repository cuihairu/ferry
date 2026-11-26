package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/tunnel"
)

// failOnceTunnel fails its first Dial then dials the landing directly (F-0813).
type failOnceTunnel struct {
	calls atomic.Int32
}

func (f *failOnceTunnel) Name() string { return "failonce-test" }
func (f *failOnceTunnel) Dial(ctx context.Context, addr string) (net.Conn, error) {
	if f.calls.Add(1) == 1 {
		return nil, errors.New("connection reset")
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}
func (f *failOnceTunnel) ServeHeartbeat(context.Context, net.Conn, time.Duration) error { return nil }
func (f *failOnceTunnel) Close() error                                                  { return nil }

func init() {
	_ = tunnel.Register("failonce-test", func(o tunnel.Options) (tunnel.Tunnel, error) {
		return &failOnceTunnel{}, nil
	})
}

// TestRelayDialRetryRecovers covers F-0813: the first landing dial fails and the
// retry succeeds within one connection, so the frontend still sees the echo.
func TestRelayDialRetryRecovers(t *testing.T) {
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
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	go func() {
		_ = run(ctx, config.RelaySpec{
			Listen: "127.0.0.1:0", LandingAddr: landing.Addr().String(),
			Tunnel: "failonce-test", DialRetries: 2,
		}, "", nil, ln, logger)
	}()

	var front net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		front, err = net.DialTimeout("tcp", ln.Addr().String(), 500*time.Millisecond)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial relay: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer front.Close()
	_ = front.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := front.Write([]byte("retry-echo")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 32)
	n, err := front.Read(got)
	if err != nil {
		t.Fatalf("read after retry: %v (log=%s)", err, buf.String())
	}
	if string(got[:n]) != "retry-echo" {
		t.Fatalf("echo = %q", got[:n])
	}
	// The recovery proves the retry path ran; the failure log itself only appears
	// when all attempts are exhausted (checked by the exhausted-attempt behavior
	// of the run loop), so assert on the absence of a fatal drop instead:
	if strings.Contains(buf.String(), "relay overloaded") || strings.Contains(buf.String(), "stream limit") {
		t.Fatalf("recovery must not drop the connection: %q", buf.String())
	}
}

// TestEffectiveRetries covers config normalization and hot-merge (F-0813).
func TestEffectiveRetries(t *testing.T) {
	if got := effectiveRetries(config.RelaySpec{}); got != defaultDialRetries {
		t.Fatalf("zero retry = %d; want %d", got, defaultDialRetries)
	}
	if got := effectiveRetries(config.RelaySpec{DialRetries: 3}); got != 3 {
		t.Fatalf("explicit retry = %d; want 3", got)
	}
	merged := mergeSpec(config.RelaySpec{DialRetries: 1}, config.RelaySpec{DialRetries: 5})
	if got := effectiveRetries(merged); got != 5 {
		t.Fatalf("merged retry = %d; want 5", got)
	}
}

// alwaysFailTunnel fails every dial (F-0813): the exhausted path logs the
// attempt count and drops just that connection without stopping the relay.
type alwaysFailTunnel struct{}

func (alwaysFailTunnel) Name() string { return "alwaysfail-test" }
func (alwaysFailTunnel) Dial(context.Context, string) (net.Conn, error) {
	return nil, errors.New("connection refused")
}
func (alwaysFailTunnel) ServeHeartbeat(context.Context, net.Conn, time.Duration) error { return nil }
func (alwaysFailTunnel) Close() error                                                  { return nil }

func init() {
	_ = tunnel.Register("alwaysfail-test", func(o tunnel.Options) (tunnel.Tunnel, error) {
		return alwaysFailTunnel{}, nil
	})
}

// TestRelayDialRetriesExhausted covers the failure path (F-0813): a gate with
// one retry logs the attempt count, drops the connection, and the relay keeps
// accepting new ones (no crash, no global failure).
func TestRelayDialRetriesExhausted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	go func() {
		_ = run(ctx, config.RelaySpec{
			Listen: "127.0.0.1:0", LandingAddr: "127.0.0.1:1",
			Tunnel: "alwaysfail-test", DialRetries: 1,
		}, "", nil, ln, logger)
	}()
	c, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	waitForLog(t, &buf, "after 2 attempts")
	// relay still accepts after the failure: no global shutdown
	c2, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("relay stopped accepting after dial failure: %v", err)
	}
	_ = c2.Close()
}
