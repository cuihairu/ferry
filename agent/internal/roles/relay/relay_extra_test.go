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

// TestRunDefaultsLoggerAndLandingRequired covers the validation order of a nil logger and a missing landing target.
func TestRunDefaultsLoggerAndLandingRequired(t *testing.T) {
	// a nil logger uses the default; a missing listen is reported first
	err := run(context.Background(), config.RelaySpec{}, "", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "listen is required") {
		t.Fatalf("err = %v; want listen is required", err)
	}
	// a listen but no landing target
	err = run(context.Background(),
		config.RelaySpec{Listen: "127.0.0.1:0"}, "", nil, nil,
		log.New(io.Discard, "", 0))
	if err == nil || !strings.Contains(err.Error(), "landing_addr is required") {
		t.Fatalf("err = %v; want landing_addr is required", err)
	}
}

// TestRunListenError covers the listen port already taken: startup fails early.
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

// TestMergeSpecOverrides covers the panel override taking only non-empty values of landing-related fields.
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
	// an empty override leaves the base untouched
	keep := mergeSpec(base, config.RelaySpec{})
	if keep != base {
		t.Fatalf("empty override must keep base: %+v", keep)
	}
}

// TestLoadOverrideMissingAndError covers a missing override file being a normal state (zero values, no error);
// a read failure (e.g. the path is a directory) must error.
func TestLoadOverrideMissingAndError(t *testing.T) {
	got, err := loadOverride(filepath.Join(t.TempDir(), "no-such.json"))
	if err != nil || got != (config.RelaySpec{}) {
		t.Fatalf("missing override = %+v, %v; want zero value nil", got, err)
	}
	if _, err := loadOverride(t.TempDir()); err == nil {
		t.Fatal("directory path must error")
	}
}

// TestTunnelPluginOfDefault covers the default transport plugin rule.
func TestTunnelPluginOfDefault(t *testing.T) {
	if got := tunnelPluginOf(config.RelaySpec{}); got != tunnel.DefaultPlugin {
		t.Fatalf("default plugin = %q; want %q", got, tunnel.DefaultPlugin)
	}
	if got := tunnelPluginOf(config.RelaySpec{Tunnel: "plain-test"}); got != "plain-test" {
		t.Fatalf("plugin = %q; want plain-test", got)
	}
}

// errTunnel is a fake plugin whose Close always fails (used by the closeAll logging branch).
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

// TestCloseAllLogsError covers one plugin failing to close while the cache closes: log it and keep clearing.
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
	// errTunnel's Close must be invoked and logged
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(buf.String(), "relay close tunnel broken") {
		if time.Now().After(deadline) {
			t.Fatalf("close error not logged: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// acceptOnceListener fails the first Accept outright, then blocks until Close (when the ctx is cancelled
// run calls Close internally, unblocking it).
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
	<-l.closed // then blocks forever until Close (or is released right after the first error)
	return nil, errors.New("listener closed")
}

func (l *acceptOnceListener) Close() error {
	l.once.Do(func() {}) // if no error has fired yet, consume once first so Close does not misreport later
	return nil
}

func (l *acceptOnceListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

// TestRunAcceptErrorLogged covers an error accepting a connection: log it and continue, never interrupting the serve loop.
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

// waitForLog polls the log buffer for a substring.
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

// TestRunHupReloadBranches covers the hot-reload branches: an override clearing the landing keeps the current one, no change means
// no switch, and after switching to an unknown plugin a new connection's dial error does not interrupt. The landing initially comes from the override
// file (the base has no landing), so that an override "clearing the landing" merges into an empty value and triggers the keep-current branch.
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
	// the startup override is written first (the base has no landing; the landing comes entirely from the override file)
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
	waitForLog(t, &buf, "relay listening on 127.0.0.1:0") // touch the override file only after startup has finished loading it

	// 1) clearing the landing: mergeSpec falls back to the base (the base landing is empty) -> keep the current one and only log
	if err := os.WriteFile(ovPath, []byte(`{"landing_addr":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	hup <- syscall.SIGHUP
	waitForLog(t, &buf, "landing_addr empty, keep current")

	// 2) identical to the current one (after falling back to the base the landing matches): no switch, no log
	if err := os.WriteFile(ovPath, []byte(`{"landing_addr":"10.1.1.1:443","tunnel":"plain-test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	hup <- syscall.SIGHUP
	time.Sleep(150 * time.Millisecond) // a mistaken switch would print repointed, so silence is the correct outcome
	if strings.Contains(buf.String(), "repointed") {
		t.Fatalf("unchanged override must not repoint: %q", buf.String())
	}

	// 3) switching to an unknown plugin: the switch succeeds and a new connection's dial failure only drops that connection
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
