package tunnel

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// TestPluginCapsDeclarations covers F-0822: every builtin plugin states its
// features through the uniform Capper surface -- camouflage plugins say so,
// plain-protocol plugins do not, and nothing claims the P1 multiplex slot or
// UDP under the P0 contract.
func TestPluginCapsDeclarations(t *testing.T) {
	for _, name := range Names() {
		tun, err := Open(name, Options{})
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		caps := CapsOf(tun)
		if !caps.Heartbeat {
			t.Errorf("%s must declare the shared heartbeat", name)
		}
		if caps.Multiplex || caps.UDP {
			t.Errorf("%s must not claim multiplex/udp under P0: %+v", name, caps)
		}
		switch name {
		case DefaultPlugin, "ws-tls":
			if !caps.Camouflage {
				t.Errorf("%s must declare camouflage", name)
			}
		case "ssh", "quic":
			if caps.Camouflage {
				t.Errorf("%s is a plain protocol on the agent leg, must not claim camouflage", name)
			}
		}
	}
}

// bareTunnel implements only the base Tunnel interface, standing in for a
// third-party or test plugin that predates Capper.
type bareTunnel struct{}

func (bareTunnel) Name() string { return "bare" }
func (bareTunnel) Dial(ctx context.Context, addr string) (net.Conn, error) {
	return nil, errors.New("bare: no dial")
}
func (bareTunnel) ServeHeartbeat(ctx context.Context, conn net.Conn, interval time.Duration) error {
	return nil
}
func (bareTunnel) Close() error { return nil }

// TestCapsOfBaseline covers the fallback: a plugin without a Caps method gets
// the P0 baseline (heartbeat only) instead of a zero declaration.
func TestCapsOfBaseline(t *testing.T) {
	caps := CapsOf(&bareTunnel{})
	if caps != (Caps{Heartbeat: true}) {
		t.Fatalf("baseline caps = %+v", caps)
	}
}
