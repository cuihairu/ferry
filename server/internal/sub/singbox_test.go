package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPackSingBox covers the sing-box outbounds array generation (built in the
// hy2 batch, the five protocols completed in the SB batch): each protocol's
// outbound fields + a minimal config omitting the optional keys + a mixed fleet
// + an error for the unsupported case.
func TestPackSingBox(t *testing.T) {
	entries := []Entry{
		{Node: storage.Node{Name: "hy", Address: "hy.example.com", Port: 443, Protocol: "hysteria2",
			Config: `{"password":"pw","sni":"s.com","obfs":"salamander","obfs_password":"op","up":100,"down":200}`}},
		{Node: storage.Node{Name: "hy-min", Address: "b", Port: 8443, Protocol: "hysteria2",
			Config: `{"password":"pw2"}`}},
	}
	out, err := PackSingBox(entries)
	if err != nil {
		t.Fatalf("PackSingBox: %v", err)
	}
	var obs []map[string]any
	if err := json.Unmarshal([]byte(out), &obs); err != nil {
		t.Fatalf("unmarshal outbounds: %v\n%s", err, out)
	}
	if len(obs) != 2 {
		t.Fatalf("outbounds = %d, want 2:\n%s", len(obs), out)
	}
	ob := obs[0]
	if ob["type"] != "hysteria2" || ob["tag"] != "hy" || ob["server"] != "hy.example.com" {
		t.Fatalf("outbound[0] head = %v", ob)
	}
	if ob["server_port"] != float64(443) || ob["password"] != "pw" || ob["sni"] != "s.com" {
		t.Fatalf("outbound[0] fields = %v", ob)
	}
	if ob["up_mbps"] != float64(100) || ob["down_mbps"] != float64(200) {
		t.Fatalf("outbound[0] bandwidth = %v", ob)
	}
	// obfs is a nested object {type, password}.
	if obfs, ok := ob["obfs"].(map[string]any); !ok || obfs["type"] != "salamander" || obfs["password"] != "op" {
		t.Fatalf("outbound[0] obfs = %v", ob["obfs"])
	}
	// Minimal config: no optional keys.
	m := obs[1]
	for _, k := range []string{"sni", "insecure", "obfs", "up_mbps", "down_mbps"} {
		if _, ok := m[k]; ok {
			t.Fatalf("outbound[1] has %s: %v", k, m)
		}
	}

	// A missing password is reported (same convention as ShareLink, the error carries the node name).
	if _, err := PackSingBox([]Entry{{Node: storage.Node{Name: "bad", Protocol: "hysteria2", Config: `{}`}}}); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("missing password err = %v", err)
	}
	// An invalid JSON config passes the error through.
	if _, err := PackSingBox([]Entry{{Node: storage.Node{Name: "x", Protocol: "hysteria2", Config: `{"password":`}}}); err == nil {
		t.Fatal("expected config parse error")
	}
}

// mustOutbounds unwraps the PackSingBox output to make per-field assertions easier.
func mustOutbounds(t *testing.T, entries []Entry) []map[string]any {
	t.Helper()
	out, err := PackSingBox(entries)
	if err != nil {
		t.Fatalf("PackSingBox: %v", err)
	}
	var obs []map[string]any
	if err := json.Unmarshal([]byte(out), &obs); err != nil {
		t.Fatalf("unmarshal outbounds: %v\n%s", err, out)
	}
	return obs
}

// TestSingBoxOutboundVless covers the vless outbound (SB batch): the full field
// set (tls + the ws transport + flow) and a minimal config (no tls/transport
// keys).
func TestSingBoxOutboundVless(t *testing.T) {
	obs := mustOutbounds(t, []Entry{
		{Node: storage.Node{Name: "vl", Address: "vl.example.com", Port: 443, Protocol: "vless",
			Config: `{"uuid":"u-1","tls":true,"sni":"s.com","net":"ws","host":"cdn.example.com","path":"/ws","flow":"xtls-rprx-vision","insecure":true}`}},
		{Node: storage.Node{Name: "vl-min", Address: "b", Port: 1, Protocol: "vless", Config: `{"uuid":"u-2"}`}},
	})
	ob := obs[0]
	if ob["type"] != "vless" || ob["uuid"] != "u-1" || ob["flow"] != "xtls-rprx-vision" {
		t.Fatalf("vless head = %v", ob)
	}
	tls, ok := ob["tls"].(map[string]any)
	if !ok || tls["enabled"] != true || tls["server_name"] != "s.com" || tls["insecure"] != true {
		t.Fatalf("vless tls = %v", ob["tls"])
	}
	tr, ok := ob["transport"].(map[string]any)
	if !ok || tr["type"] != "ws" || tr["path"] != "/ws" {
		t.Fatalf("vless transport = %v", ob["transport"])
	}
	if h, ok := tr["headers"].(map[string]any); !ok || h["Host"] != "cdn.example.com" {
		t.Fatalf("vless transport headers = %v", tr["headers"])
	}
	// Minimal config: no tls/transport/flow.
	m := obs[1]
	if m["uuid"] != "u-2" {
		t.Fatalf("vless min uuid = %v", m)
	}
	for _, k := range []string{"tls", "transport", "flow"} {
		if _, ok := m[k]; ok {
			t.Fatalf("vless min has %s: %v", k, m)
		}
	}
	// A missing uuid is reported.
	if _, err := OutboundOf(&storage.Node{Name: "x", Protocol: "vless", Config: `{}`}); err == nil {
		t.Fatal("expected vless uuid error")
	}
}

// TestSingBoxOutboundVmess covers the vmess outbound (SB batch): the scy default
// of auto, alter_id, tls; a minimal config has no tls/transport.
func TestSingBoxOutboundVmess(t *testing.T) {
	obs := mustOutbounds(t, []Entry{
		{Node: storage.Node{Name: "vm", Address: "a", Port: 443, Protocol: "vmess",
			Config: `{"uuid":"u-1","scy":"aes-128-gcm","aid":64,"tls":true,"sni":"s.com","net":"grpc","path":"srv"}`}},
		{Node: storage.Node{Name: "vm-min", Address: "b", Port: 1, Protocol: "vmess", Config: `{"uuid":"u-2"}`}},
	})
	ob := obs[0]
	if ob["security"] != "aes-128-gcm" || ob["alter_id"] != float64(64) || ob["uuid"] != "u-1" {
		t.Fatalf("vmess fields = %v", ob)
	}
	if tls, ok := ob["tls"].(map[string]any); !ok || tls["server_name"] != "s.com" {
		t.Fatalf("vmess tls = %v", ob["tls"])
	}
	if tr, ok := ob["transport"].(map[string]any); !ok || tr["type"] != "grpc" || tr["service_name"] != "srv" {
		t.Fatalf("vmess transport = %v", ob["transport"])
	}
	m := obs[1]
	if m["security"] != "auto" || m["alter_id"] != float64(0) {
		t.Fatalf("vmess min = %v", m)
	}
	for _, k := range []string{"tls", "transport"} {
		if _, ok := m[k]; ok {
			t.Fatalf("vmess min has %s: %v", k, m)
		}
	}
	if _, err := OutboundOf(&storage.Node{Name: "x", Protocol: "vmess", Config: `{}`}); err == nil {
		t.Fatal("expected vmess uuid error")
	}
}

// TestSingBoxOutboundTrojanSS covers trojan (always TLS) and shadowsocks (SB batch).
func TestSingBoxOutboundTrojanSS(t *testing.T) {
	obs := mustOutbounds(t, []Entry{
		{Node: storage.Node{Name: "tj", Address: "a", Port: 443, Protocol: "trojan",
			Config: `{"password":"pw","sni":"s.com"}`}},
		{Node: storage.Node{Name: "ss", Address: "b", Port: 8388, Protocol: "shadowsocks",
			Config: `{"method":"aes-256-gcm","password":"pw"}`}},
	})
	tj := obs[0]
	if tj["password"] != "pw" {
		t.Fatalf("trojan = %v", tj)
	}
	// trojan is TLS by its own protocol: tls.enabled=true is emitted even when cfg.tls is not written, and sni goes into server_name.
	if tls, ok := tj["tls"].(map[string]any); !ok || tls["enabled"] != true || tls["server_name"] != "s.com" {
		t.Fatalf("trojan tls = %v", tj["tls"])
	}
	ss := obs[1]
	if ss["method"] != "aes-256-gcm" || ss["password"] != "pw" {
		t.Fatalf("ss = %v", ss)
	}
	if _, ok := ss["tls"]; ok {
		t.Fatalf("ss must not have tls: %v", ss)
	}
	// Error branches.
	if _, err := OutboundOf(&storage.Node{Name: "x", Protocol: "trojan", Config: `{}`}); err == nil {
		t.Fatal("expected trojan password error")
	}
	if _, err := OutboundOf(&storage.Node{Name: "x", Protocol: "shadowsocks", Config: `{"password":"pw"}`}); err == nil {
		t.Fatal("expected ss method error")
	}
}

// TestSingBoxOutboundNetErrors covers the explicit error for an unknown net and the http/httpupgrade mappings.
func TestSingBoxOutboundNetErrors(t *testing.T) {
	if _, err := OutboundOf(&storage.Node{Name: "x", Protocol: "vless", Config: `{"uuid":"u","net":"kcp"}`}); err == nil || !strings.Contains(err.Error(), "kcp") {
		t.Fatalf("unknown net err = %v", err)
	}
	for _, net := range []string{"http", "httpupgrade"} {
		ob, err := OutboundOf(&storage.Node{Name: "x", Address: "a", Port: 1, Protocol: "vless",
			Config: `{"uuid":"u","net":"` + net + `","host":"h.com","path":"/p"}`})
		if err != nil {
			t.Fatalf("net %s: %v", net, err)
		}
		tr := ob["transport"].(map[string]any)
		if tr["type"] != net || tr["path"] != "/p" {
			t.Fatalf("net %s transport = %v", net, tr)
		}
	}
	// tcp / the default emit no transport.
	ob, err := OutboundOf(&storage.Node{Name: "x", Address: "a", Port: 1, Protocol: "vless", Config: `{"uuid":"u","net":"tcp"}`})
	if err != nil {
		t.Fatalf("tcp: %v", err)
	}
	if _, ok := ob["transport"]; ok {
		t.Fatalf("tcp must not have transport: %v", ob)
	}
}

// TestPackSingBoxMixedFleet covers a mixed fleet (the core SB-batch scenario):
// all five protocols coexist and come out in one pass, no longer failing the
// whole pack because of a single xray-family node.
func TestPackSingBoxMixedFleet(t *testing.T) {
	entries := []Entry{
		{Node: storage.Node{Name: "vl", Address: "a", Port: 1, Protocol: "vless", Config: `{"uuid":"u1"}`}},
		{Node: storage.Node{Name: "vm", Address: "b", Port: 2, Protocol: "vmess", Config: `{"uuid":"u2"}`}},
		{Node: storage.Node{Name: "tj", Address: "c", Port: 3, Protocol: "trojan", Config: `{"password":"p3"}`}},
		{Node: storage.Node{Name: "ss", Address: "d", Port: 4, Protocol: "shadowsocks", Config: `{"method":"aes-256-gcm","password":"p4"}`}},
		{Node: storage.Node{Name: "hy", Address: "e", Port: 5, Protocol: "hysteria2", Config: `{"password":"p5"}`}},
	}
	obs := mustOutbounds(t, entries)
	if len(obs) != 5 {
		t.Fatalf("outbounds = %d, want 5", len(obs))
	}
	for i, want := range []string{"vless", "vmess", "trojan", "shadowsocks", "hysteria2"} {
		if obs[i]["type"] != want {
			t.Fatalf("outbound[%d] type = %v, want %s", i, obs[i]["type"], want)
		}
	}
	// An unsupported protocol still fails the whole pack (same convention as clash/v2ray, never silently swallow a node).
	if _, err := PackSingBox([]Entry{{Node: storage.Node{Name: "x", Protocol: "ssr", Config: `{}`}}}); err == nil {
		t.Fatal("expected unsupported protocol error")
	}
}
