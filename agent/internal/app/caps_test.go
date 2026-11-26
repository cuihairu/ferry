package app

import (
	"reflect"
	"testing"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestStaticCaps covers F-0228's derivation: the declared set follows what the
// config actually runs, not just the role.
func TestStaticCaps(t *testing.T) {
	core := config.Config{
		Meta:   agentproto.NodeMeta{Role: agentproto.RoleEntry},
		Procs:  []config.ProcSpec{{Name: "xray", Exec: "xray"}},
		Probes: []config.ProbeSpec{{Name: "p", Target: "a:443"}},
	}
	got := staticCaps(core)
	want := []string{agentproto.CapProxyIngress, agentproto.CapProbe}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entry core caps = %v", got)
	}

	relay := config.Config{Relay: config.RelaySpec{Listen: "127.0.0.1:1080"}}
	if got = staticCaps(relay); !reflect.DeepEqual(got, []string{agentproto.CapRelayEntry}) {
		t.Fatalf("relay caps = %v", got)
	}

	landing := config.Config{Meta: agentproto.NodeMeta{Role: agentproto.RoleLanding}}
	if got = staticCaps(landing); !reflect.DeepEqual(got, []string{agentproto.CapRelayLanding}) {
		t.Fatalf("landing caps = %v", got)
	}

	// Nothing configured: empty set (the panel falls back to the role default).
	if got = staticCaps(config.Config{}); len(got) != 0 {
		t.Fatalf("bare config caps = %v", got)
	}
}

// TestDynamicCaps covers F-0229: proxy ingress follows the live proc states,
// declared role surfaces report ok while the core is up.
func TestDynamicCaps(t *testing.T) {
	cfg := config.Config{
		Procs:  []config.ProcSpec{{Name: "xray", Exec: "xray"}},
		Probes: []config.ProbeSpec{{Name: "p", Target: "a:443"}},
		Relay:  config.RelaySpec{Listen: "127.0.0.1:1080"},
	}
	up := []agentproto.ProcStatus{{Name: "xray", State: agentproto.ProcRunning}}
	got := dynamicCaps(cfg, up)
	want := map[string]string{
		agentproto.CapProbe:        agentproto.CapStateOK,
		agentproto.CapRelayEntry:   agentproto.CapStateOK,
		agentproto.CapProxyIngress: agentproto.CapStateOK,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("all-running caps = %v", got)
	}

	got = dynamicCaps(cfg, []agentproto.ProcStatus{
		{Name: "xray", State: agentproto.ProcRunning},
		{Name: "hy2", State: agentproto.ProcCrashed},
	})
	if got[agentproto.CapProxyIngress] != agentproto.CapStateDegraded {
		t.Fatalf("mixed crash state = %v", got)
	}

	got = dynamicCaps(cfg, []agentproto.ProcStatus{{Name: "xray", State: agentproto.ProcStopped}})
	if got[agentproto.CapProxyIngress] != agentproto.CapStateOff {
		t.Fatalf("all-down state = %v", got)
	}

	// Nothing managed or declared: nil (absent keys never disqualify).
	if got = dynamicCaps(config.Config{}, nil); got != nil {
		t.Fatalf("bare dynamic caps = %v", got)
	}
}

// TestRelayCaps covers F-0804's heartbeat declaration: nil without a relay
// listen, TCP/reconnect always on, UDP always off (landing side cannot
// terminate it), feature flags only when the spec carries a positive value,
// and the MaxConns fallback to the package default.
func TestRelayCaps(t *testing.T) {
	if got := relayCaps(config.Config{}); got != nil {
		t.Fatalf("no relay listen = %v, want nil", got)
	}

	caps := relayCaps(config.Config{Relay: config.RelaySpec{Listen: "0.0.0.0:8443"}})
	if caps == nil {
		t.Fatal("relay listen set = nil caps")
	}
	if !caps.TCPForward || !caps.Reconnect {
		t.Fatalf("tcp/reconnect must be on: %+v", caps)
	}
	if caps.UDPForward {
		t.Fatal("udp_forward must stay off until landing-side UDP termination")
	}
	if caps.IdleCleanup || caps.Metrics || caps.StreamLimit || caps.BandwidthLimit {
		t.Fatalf("zero limits must not be declared: %+v", caps)
	}
	if caps.MaxConns != 128 || caps.MaxStreams != 0 || caps.RateLimitKbps != 0 {
		t.Fatalf("default limits wrong: %+v", caps)
	}

	caps = relayCaps(config.Config{Relay: config.RelaySpec{
		Listen:         "0.0.0.0:8443",
		MaxConns:       64,
		MaxStreams:     256,
		RateLimitKbps:  2048,
		IdleTimeoutSec: 30,
		MetricsListen:  "127.0.0.1:10090",
	}})
	if !caps.IdleCleanup || !caps.Metrics || !caps.StreamLimit || !caps.BandwidthLimit {
		t.Fatalf("configured limits must be declared: %+v", caps)
	}
	if caps.MaxConns != 64 || caps.MaxStreams != 256 || caps.RateLimitKbps != 2048 {
		t.Fatalf("configured values not carried: %+v", caps)
	}
}
