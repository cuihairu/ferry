package sub

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// capsNode is the shared node for the matrix/adapter agreement tests.
func capsNode(udp bool) storage.Node {
	return storage.Node{Name: "cap", Address: "c.example.com", Port: 443,
		Protocol: "trojan", Config: `{"password":"pw"}`, UdpOpen: udp}
}

// TestClientCapsMatrix covers the matrix itself (F-1127): one row per delivery
// target, unknown targets miss, and mihomo is the clash superset.
func TestClientCapsMatrix(t *testing.T) {
	for _, target := range []string{"v2ray", "clash", "mihomo", "xray", "singbox"} {
		if _, ok := CapsFor(target); !ok {
			t.Fatalf("matrix missing target %s", target)
		}
	}
	if _, ok := CapsFor("nope"); ok {
		t.Fatal("unknown target must miss")
	}
	m, _ := CapsFor("mihomo")
	if !m.Notes || !m.UDP || !m.PacketEncoding || !m.LazyGroups || !m.RegionGroups {
		t.Fatalf("mihomo row = %+v", m)
	}
	c, _ := CapsFor("clash")
	if c.PacketEncoding || c.LazyGroups || c.JSON {
		t.Fatalf("clash row must lack the meta-only features: %+v", c)
	}
	v, _ := CapsFor("v2ray")
	if v.UDP || v.RegionGroups || v.JSON {
		t.Fatalf("v2ray row = %+v", v)
	}
	x, _ := CapsFor("xray")
	s, _ := CapsFor("singbox")
	if !x.JSON || !s.JSON || x.Notes || s.Notes {
		t.Fatalf("json rows = %+v / %+v", x, s)
	}
}

// TestCapsMatchAdapters keeps the matrix honest against the adapters: every
// row's claim is checked against what its pack function actually emits
// (F-1127).
func TestCapsMatchAdapters(t *testing.T) {
	// clash family: the udp declaration and the comment channel agree with the row.
	for target, pack := range map[string]func([]Entry, []string) (string, error){
		"clash": PackClash, "mihomo": PackMihomo,
	} {
		caps, _ := CapsFor(target)
		out, err := pack([]Entry{{Node: capsNode(true)}}, []string{"note"})
		if err != nil {
			t.Fatal(err)
		}
		if caps.UDP && !strings.Contains(out, "udp: true") {
			t.Fatalf("%s: udp row claim violated:\n%s", target, out)
		}
		if caps.Notes && !strings.Contains(out, "# note") {
			t.Fatalf("%s: notes row claim violated:\n%s", target, out)
		}
	}
	// v2ray family: notes survive (base64 body), no udp/region-group surface.
	caps, _ := CapsFor("v2ray")
	out, err := PackV2Ray([]Entry{{Node: capsNode(true)}}, []string{"note"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(out)
	if err != nil {
		t.Fatal(err)
	}
	if caps.Notes && !strings.Contains(string(raw), "# note") {
		t.Fatalf("v2ray: notes row claim violated: %q", string(raw))
	}
	// json targets: plain JSON, no comment position -- the row's JSON=true and
	// Notes=false agree with the packs' silent treatment of the notes.
	for target, pack := range map[string]func([]Entry, []string) (string, error){
		"xray":    func(e []Entry, _ []string) (string, error) { return PackXray(e) },
		"singbox": func(e []Entry, _ []string) (string, error) { return PackSingBox(e) },
	} {
		caps, _ := CapsFor(target)
		out, err := pack([]Entry{{Node: capsNode(true)}}, []string{"note"})
		if err != nil {
			t.Fatal(err)
		}
		if caps.JSON && strings.Contains(out, "//") {
			t.Fatalf("%s: json body must not carry comments:\n%s", target, out)
		}
	}
}
