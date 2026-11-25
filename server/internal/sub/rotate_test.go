package sub

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

func ventry(name, region string, rtt int) Entry {
	return Entry{
		Node:  storage.Node{Name: name, Address: name + ".example.com", Port: 1, Protocol: "vless", Region: region, Config: `{"uuid":"u"}`},
		RttMs: rtt,
	}
}

func entryNames(entries []Entry) []string {
	out := make([]string, len(entries))
	for i := range entries {
		out[i] = entries[i].Node.Name
	}
	return out
}

func assertNames(t *testing.T, got []Entry, want ...string) {
	t.Helper()
	gotNames := entryNames(got)
	if len(gotNames) != len(want) {
		t.Fatalf("order = %v, want %v", gotNames, want)
	}
	for i := range want {
		if gotNames[i] != want[i] {
			t.Fatalf("order = %v, want %v", gotNames, want)
		}
	}
}

// TestOrderByProbeRTT covers probe-verdict ordering: known RTT ascending first,
// entries without probe data (0) last and keeping original order.
func TestOrderByProbeRTT(t *testing.T) {
	now := time.Unix(rotateWindowSec*10, 0)
	entries := []Entry{
		ventry("a", "香港", 0),   // no data
		ventry("b", "香港", 150), // known
		ventry("c", "香港", 50),  // known, lower
		ventry("d", "香港", 0),   // no data
	}
	got := orderByProbe(entries, now)
	assertNames(t, got, "c", "b", "a", "d")
}

// TestOrderByProbeRotation covers rotation within the same RTT tier: order fixed inside the window,
// shifting one step across windows (injecting now to verify stateless derivation).
func TestOrderByProbeRotation(t *testing.T) {
	entries := []Entry{
		ventry("x", "香港", 100),
		ventry("y", "香港", 100),
		ventry("z", "香港", 100),
	}
	assertNames(t, orderByProbe(entries, time.Unix(0, 0)), "x", "y", "z")
	assertNames(t, orderByProbe(entries, time.Unix(rotateWindowSec, 0)), "y", "z", "x")
	assertNames(t, orderByProbe(entries, time.Unix(2*rotateWindowSec, 0)), "z", "x", "y")
	// Back in the same window: order restored (deterministic, no persisted state).
	assertNames(t, orderByProbe(entries, time.Unix(0, 0)), "x", "y", "z")
}

// TestOrderByProbeUnknownStable covers no rotation when there is no data at all: original order kept.
func TestOrderByProbeUnknownStable(t *testing.T) {
	entries := []Entry{
		ventry("p", "香港", 0),
		ventry("q", "香港", 0),
		ventry("r", "香港", 0),
	}
	for _, now := range []time.Time{time.Unix(0, 0), time.Unix(rotateWindowSec, 0), time.Unix(3*rotateWindowSec, 0)} {
		assertNames(t, orderByProbe(entries, now), "p", "q", "r")
	}
}

// TestOrderByProbeRegionGroup covers region grouping: no interleaving across regions,
// each group sorted by its own verdicts.
func TestOrderByProbeRegionGroup(t *testing.T) {
	now := time.Unix(0, 0)
	entries := []Entry{
		ventry("u1", "美国", 90),
		ventry("h1", "香港", 200),
		ventry("h2", "香港", 100),
		ventry("u2", "美国", 0),
	}
	got := orderByProbe(entries, now)
	// Regions grouped by string (US < HK); within a group, known first by ascending RTT.
	assertNames(t, got, "u1", "u2", "h2", "h1")
}

// TestOrderByProbeEdge covers the empty list and a single entry.
func TestOrderByProbeEdge(t *testing.T) {
	if got := orderByProbe(nil, time.Unix(0, 0)); len(got) != 0 {
		t.Fatalf("empty = %v", got)
	}
	single := []Entry{ventry("only", "香港", 80)}
	assertNames(t, orderByProbe(single, time.Unix(rotateWindowSec, 0)), "only")
}

// TestPackV2RayProbeOrder covers v2ray subscription integration: within the same region, lower-RTT
// entries come first (clients usually take the first entry).
func TestPackV2RayProbeOrder(t *testing.T) {
	entries := []Entry{
		ventry("slow", "香港", 300),
		ventry("fast", "香港", 80),
	}
	packed, err := PackV2Ray(entries, nil)
	if err != nil {
		t.Fatalf("PackV2Ray: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(packed)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2: %q", len(lines), string(raw))
	}
	if !strings.HasPrefix(lines[0], "vless://u@fast.example.com") {
		t.Fatalf("first line = %q, want fast entry first", lines[0])
	}
	if !strings.HasPrefix(lines[1], "vless://u@slow.example.com") {
		t.Fatalf("second line = %q, want slow entry second", lines[1])
	}
}
