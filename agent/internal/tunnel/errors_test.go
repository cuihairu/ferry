package tunnel

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestClassify covers F-0823's read side: the sentinel, every wrapped class,
// chain penetration through fmt.Errorf, and the network fallback.
func TestClassify(t *testing.T) {
	if got := Classify(nil); got != "" {
		t.Fatalf("nil error class = %q", got)
	}
	if got := Classify(ErrHeartbeatLost); got != ClassHeartbeat {
		t.Fatalf("sentinel class = %q", got)
	}
	if got := Classify(fmt.Errorf("wrapped: %w", ErrHeartbeatLost)); got != ClassHeartbeat {
		t.Fatalf("sentinel through wrap class = %q", got)
	}
	cases := map[ErrorClass]error{
		ClassConfig:    Errorf(ClassConfig, "ssh:dial", "requires key_file"),
		ClassAuth:      Wrap(ClassAuth, "quic:dial", errors.New("ERR unreachable")),
		ClassNetwork:   Wrap(ClassNetwork, "tls-camo:dial", errors.New("refused")),
		ClassHeartbeat: Wrap(ClassHeartbeat, "", errors.New("frame timeout")),
	}
	for want, err := range cases {
		if got := Classify(err); got != want {
			t.Errorf("Classify(%v) = %q, want %q", err, got, want)
		}
	}
	// unclassified third-party errors fall back to the retryable default
	if got := Classify(errors.New("mystery")); got != ClassNetwork {
		t.Fatalf("fallback class = %q", got)
	}
	// class survives additional fmt.Errorf wrapping
	double := fmt.Errorf("relay: %w", Wrap(ClassConfig, "ssh:dial", errors.New("bad key")))
	if got := Classify(double); got != ClassConfig {
		t.Fatalf("double-wrapped class = %q", got)
	}
	// Error/Wrap render op and cause
	e := Wrap(ClassAuth, "quic:dial", errors.New("ERR nope")).(*Error)
	if e.Op != "quic:dial" || e.Unwrap().Error() != "ERR nope" {
		t.Fatalf("wrap fields: %+v", e)
	}
	if Wrap(ClassConfig, "x:dial", nil) != nil {
		t.Fatal("Wrap(nil) must stay nil")
	}
}

// TestPluginErrorClasses drives real plugin error paths end to end: bad plugin
// options surface as config trouble, unreachable peers as network trouble.
func TestPluginErrorClasses(t *testing.T) {
	// ssh: missing options -> config (no dial attempted)
	sSSH, err := Open("ssh", Options{})
	if err != nil {
		t.Fatalf("open ssh: %v", err)
	}
	if _, err := sSSH.Dial(context.Background(), "127.0.0.1:1"); Classify(err) != ClassConfig {
		t.Fatalf("ssh missing options class = %q (%v)", Classify(err), err)
	}

	// tls-camo: unreadable CA file -> config
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if _, err := Open(DefaultPlugin, Options{CAFile: ca}); err != nil {
		t.Fatalf("open %s: %v", DefaultPlugin, err)
	}
	sCamo, err := Open(DefaultPlugin, Options{CAFile: ca})
	if err != nil {
		t.Fatalf("open %s: %v", DefaultPlugin, err)
	}
	_, dialErr := sCamo.Dial(context.Background(), "127.0.0.1:1")
	if Classify(dialErr) != ClassConfig {
		t.Fatalf("missing CA file class = %q (%v)", Classify(dialErr), dialErr)
	}

	// wstls: same CA rule under a different plugin name
	sWS, err := Open("ws-tls", Options{CAFile: ca})
	if err != nil {
		t.Fatalf("open ws-tls: %v", err)
	}
	_, wsErr := sWS.Dial(context.Background(), "127.0.0.1:1")
	if Classify(wsErr) != ClassConfig {
		t.Fatalf("ws-tls missing CA class = %q (%v)", Classify(wsErr), wsErr)
	}

	// quic: no sidecar listening -> network
	sQUIC, err := Open("quic", Options{Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("open quic: %v", err)
	}
	_, quicErr := sQUIC.Dial(context.Background(), "127.0.0.1:1")
	if Classify(quicErr) != ClassNetwork {
		t.Fatalf("quic no sidecar class = %q (%v)", Classify(quicErr), quicErr)
	}
}
