package sub

import (
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestClashEntryOfValidationErrors covers the rejection surface: each protocol missing required fields / config not JSON.
func TestClashEntryOfValidationErrors(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		config   string
		wantErr  string
	}{
		{"config not json", "vless", "{oops", "parse node config"},
		{"vless no uuid", "vless", `{}`, "vless requires config.uuid"},
		{"vmess no uuid", "vmess", `{}`, "vmess requires config.uuid"},
		{"trojan no password", "trojan", `{}`, "trojan requires config.password"},
		{"ss no method", "shadowsocks", `{"password":"pw"}`, "shadowsocks requires config.method"},
		{"ss no password", "shadowsocks", `{"method":"aes-128-gcm"}`, "shadowsocks requires config.password"},
		{"hysteria2 no password", "hysteria2", `{}`, "hysteria2 requires config.password"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := clashEntryOf(&storage.Node{Name: "n", Address: "a", Port: 443,
				Protocol: c.protocol, Config: c.config})
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v; want contains %q", err, c.wantErr)
			}
		})
	}
}

// TestPackClashUdpDeclaration covers the unified UDP support declaration
// (F-1106): a node with UdpOpen emits udp: true in the clash YAML, an
// undeclared node stays without the key (byte-identical to before).
func TestPackClashUdpDeclaration(t *testing.T) {
	entries := []Entry{
		{Node: storage.Node{Name: "udp-on", Address: "u1.example.com", Port: 443,
			Protocol: "trojan", Config: `{"password":"pw"}`, UdpOpen: true}},
		{Node: storage.Node{Name: "udp-off", Address: "u2.example.com", Port: 443,
			Protocol: "trojan", Config: `{"password":"pw"}`}},
	}
	out, err := PackClash(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "udp: true") {
		t.Fatalf("declared node must emit udp: true:\n%s", out)
	}
	off := out[:strings.Index(out, "name: udp-on")]
	if strings.Contains(off, "udp:") {
		t.Fatalf("undeclared node must stay silent:\n%s", off)
	}
}
