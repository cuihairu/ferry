package relay

import (
	"bytes"
	"context"
	"log"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/tunnel"
)

// blockTunnel is a plugin whose Dial parks until released, holding a forwarded
// connection open so tests can observe the connection ceiling.
type blockTunnel struct {
	started     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	startOnce   sync.Once
}

func (b *blockTunnel) Name() string { return "block-test" }
func (b *blockTunnel) Dial(_ context.Context, _ string) (net.Conn, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.release
	return nil, context.Canceled // after release the forwarding simply ends
}
func (b *blockTunnel) ServeHeartbeat(context.Context, net.Conn, time.Duration) error {
	return nil
}
func (b *blockTunnel) Close() error { return nil }

func init() {
	_ = tunnel.Register("block-test", func(o tunnel.Options) (tunnel.Tunnel, error) {
		return &blockTunnel{started: make(chan struct{}), release: make(chan struct{})}, nil
	})
}

// TestRelayMaxConnsRefusesOverflow covers F-0808: with max_conns=1 a second
// frontend connection is refused outright ("overloaded" drop) while the first
// is being forwarded; releasing the blocked dial frees the slot.
func TestRelayMaxConnsRefusesOverflow(t *testing.T) {
	bt := &blockTunnel{started: make(chan struct{}), release: make(chan struct{})}
	_ = tunnel.Register("block-cap-test", func(o tunnel.Options) (tunnel.Tunnel, error) {
		return bt, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	runErr := make(chan error, 1)
	go func() {
		runErr <- run(ctx, config.RelaySpec{
			Listen: "127.0.0.1:0", LandingAddr: "127.0.0.1:1",
			Tunnel: "block-cap-test", MaxConns: 1,
		}, "", nil, ln, logger)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(buf.String(), "relay listening") {
		if time.Now().After(deadline) {
			t.Fatalf("relay never started: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() {
		bt.releaseOnce.Do(func() { close(bt.release) })
		cancel()
		select {
		case <-runErr:
		case <-time.After(3 * time.Second):
		}
	})

	dial := func() net.Conn {
		t.Helper()
		c, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
		if err != nil {
			t.Fatalf("dial relay: %v", err)
		}
		return c
	}

	// the first connection occupies the single slot: its dial blocks inside the plugin
	first := dial()
	defer first.Close()
	select {
	case <-bt.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first connection never reached the plugin")
	}
	// the second connection is refused before it can reach the plugin
	second := dial()
	defer second.Close()
	waitForLog(t, &buf, "relay overloaded")

	// releasing the plugin dial frees the slot; a fresh connection is accepted
	// and reaches the plugin again (the same instance returns immediately now).
	bt.releaseOnce.Do(func() { close(bt.release) })
	third := dial()
	defer third.Close()
	waitForLog(t, &buf, "relay dial")
}

// TestEffectiveMaxConns covers the default fallback (F-0808): zero and negative
// values both fall back to the built-in ceiling; positive values pass through.
func TestEffectiveMaxConns(t *testing.T) {
	if got := effectiveMaxConns(config.RelaySpec{}); got != defaultMaxConns {
		t.Fatalf("zero fallback = %d; want %d", got, defaultMaxConns)
	}
	if got := effectiveMaxConns(config.RelaySpec{MaxConns: -3}); got != defaultMaxConns {
		t.Fatalf("negative fallback = %d; want %d", got, defaultMaxConns)
	}
	if got := effectiveMaxConns(config.RelaySpec{MaxConns: 7}); got != 7 {
		t.Fatalf("explicit limit = %d; want 7", got)
	}
}

// TestMergeSpecMaxConns covers hot-reload semantics: an override carrying a
// limit changes it, while an empty override keeps the base value.
func TestMergeSpecMaxConns(t *testing.T) {
	base := config.RelaySpec{Listen: "127.0.0.1:1080", MaxConns: 5}
	if got := effectiveMaxConns(mergeSpec(base, config.RelaySpec{})); got != 5 {
		t.Fatalf("empty override changed limit: %d", got)
	}
	if got := effectiveMaxConns(mergeSpec(base, config.RelaySpec{MaxConns: 9})); got != 9 {
		t.Fatalf("override limit not applied: %d", got)
	}
}

// TestRelayMaxStreamsRefusesOverflow covers F-0809: with max_conns=10 but
// max_streams=1 the second frontend connection is dropped for stream exhaustion
// while the first backend dial is still blocked, proving the two caps are
// independent resources.
func TestRelayMaxStreamsRefusesOverflow(t *testing.T) {
	bt := &blockTunnel{started: make(chan struct{}), release: make(chan struct{})}
	_ = tunnel.Register("block-stream-test", func(o tunnel.Options) (tunnel.Tunnel, error) {
		return bt, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	runErr := make(chan error, 1)
	go func() {
		runErr <- run(ctx, config.RelaySpec{
			Listen: "127.0.0.1:0", LandingAddr: "127.0.0.1:1",
			Tunnel: "block-stream-test", MaxConns: 10, MaxStreams: 1,
		}, "", nil, ln, logger)
	}()
	t.Cleanup(func() {
		bt.releaseOnce.Do(func() { close(bt.release) })
		cancel()
		select {
		case <-runErr:
		case <-time.After(3 * time.Second):
		}
	})
	dial := func() net.Conn {
		c, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
		if err != nil {
			t.Fatalf("dial relay: %v", err)
		}
		return c
	}
	first := dial()
	defer first.Close()
	select {
	case <-bt.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first connection never reached the plugin")
	}
	second := dial()
	defer second.Close()
	waitForLog(t, &buf, "relay stream limit reached")
}
