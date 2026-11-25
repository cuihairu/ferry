package storage

import (
	"reflect"
	"testing"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestStaticCapsAndGate covers F-0222~F-0227 + F-0230 at the model level: the
// stored list wins, the role-derived default fills an empty one, and the
// dynamic state can turn a declared capability off.
func TestStaticCapsAndGate(t *testing.T) {
	entry := Node{Role: "entry"}
	if got := StaticCaps(entry); !reflect.DeepEqual(got,
		[]string{CapProxyIngress, CapRelayEntry, CapProbe}) {
		t.Fatalf("entry default caps = %v", got)
	}
	landing := Node{Role: "landing"}
	if got := StaticCaps(landing); !reflect.DeepEqual(got, []string{CapRelayLanding}) {
		t.Fatalf("landing default caps = %v", got)
	}
	both := Node{Role: "both"}
	if got := StaticCaps(both); len(got) != 4 {
		t.Fatalf("both default caps = %v", got)
	}

	// A panel-stored list wins over the role default.
	panel := Node{Role: "landing", Capabilities: `["proxy_ingress","tcp_forward"]`}
	if got := StaticCaps(panel); !reflect.DeepEqual(got, []string{CapProxyIngress, CapTCPForward}) {
		t.Fatalf("panel caps = %v", got)
	}
	if HasCapability(panel, CapRelayLanding) {
		t.Fatal("panel list without relay_landing must fail the landing gate")
	}

	// Static coverage is required...
	if HasCapability(landing, CapRelayEntry) {
		t.Fatal("landing does not cover relay_entry")
	}
	// ...but an absent dynamic state does not disqualify.
	if !HasCapability(landing, CapRelayLanding) {
		t.Fatal("unknown dynamic state must pass")
	}
	// A degraded state still routes; only off excludes.
	dyn := Node{Role: "landing", CapabilityState: `{"relay_landing":"off"}`}
	if HasCapability(dyn, CapRelayLanding) {
		t.Fatal("off state must fail the gate")
	}
	dyn.CapabilityState = `{"relay_landing":"degraded"}`
	if !HasCapability(dyn, CapRelayLanding) {
		t.Fatal("degraded state must still allocate")
	}
}

// TestMergeDynamic covers F-0229's state folding: override on report, keep on
// absence, and report exactly the changed capabilities for history (F-0231).
func TestMergeDynamic(t *testing.T) {
	cur := map[string]string{CapProxyIngress: "ok", CapProbe: "ok"}
	merged, changed := MergeDynamic(cur, map[string]string{
		CapProxyIngress: "degraded",
		CapProbe:        "ok",
		CapRelayEntry:   "off",
	})
	want := map[string]string{CapProxyIngress: "degraded", CapProbe: "ok", CapRelayEntry: "off"}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %v", merged)
	}
	if !reflect.DeepEqual(changed, []string{CapProxyIngress, CapRelayEntry}) &&
		!reflect.DeepEqual(changed, []string{CapRelayEntry, CapProxyIngress}) {
		t.Fatalf("changed = %v", changed)
	}
	// No report = no change.
	if _, changed = MergeDynamic(cur, map[string]string{CapProbe: "ok"}); len(changed) != 0 {
		t.Fatalf("unchanged report yielded %v", changed)
	}
}

// TestParseCapsJSON covers the naming contract (F-0223~F-0227): unknown names
// are rejected, duplicates are removed, and the output is canonical JSON.
func TestParseCapsJSON(t *testing.T) {
	raw, err := ParseCapsJSON([]string{CapProxyIngress, CapProbe, CapProxyIngress})
	if err != nil {
		t.Fatal(err)
	}
	if raw != `["proxy_ingress","probe"]` {
		t.Fatalf("canonical caps = %s", raw)
	}
	if _, err := ParseCapsJSON([]string{"made_up"}); err == nil {
		t.Fatal("unknown capability must be rejected")
	}
	if raw, err = ParseCapsJSON([]string{}); err != nil || raw != "[]" {
		t.Fatalf("empty caps = %q err=%v", raw, err)
	}
	// Every declared name is accepted (F-0227 includes tcp/udp forward).
	if _, err := ParseCapsJSON([]string{
		CapProxyIngress, CapRelayEntry, CapRelayLanding,
		CapProbe, CapTCPForward, CapUDPForward,
	}); err != nil {
		t.Fatalf("declared capability rejected: %v", err)
	}
}

// TestJSONStateAndDynamicParse pins the stored representation: nil state is an
// empty string, and a corrupt value parses as unknown (nil).
func TestJSONStateAndDynamicParse(t *testing.T) {
	if JSONState(nil) != "" {
		t.Fatal("nil state must marshal to empty")
	}
	state := map[string]string{CapProbe: "ok"}
	n := Node{CapabilityState: JSONState(state)}
	if got := DynamicState(n); !reflect.DeepEqual(got, state) {
		t.Fatalf("state round-trip = %v", got)
	}
	n.CapabilityState = "{not json"
	if DynamicState(n) != nil {
		t.Fatal("corrupt state must parse as unknown")
	}
}

// TestDeriveCapsFromMeta mirrors the role fallback on the shared contract.
func TestDeriveCapsFromMeta(t *testing.T) {
	got := agentproto.DeriveCapsFromMeta(agentproto.NodeMeta{Role: agentproto.RoleBoth})
	want := []string{CapProxyIngress, CapRelayEntry, CapProbe, CapRelayLanding}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("both meta caps = %v", got)
	}
}
