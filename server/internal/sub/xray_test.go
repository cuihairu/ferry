package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPackXray covers the xray-json outbounds array (G1): the parse-back of a
// mixed fleet plus one assertion per protocol's own outbound schema.
func TestPackXray(t *testing.T) {
	entries := []Entry{
		{Node: storage.Node{Name: "vl", Address: "vl.example.com", Port: 443, Protocol: "vless",
			Config: `{"uuid":"u-1","tls":true,"sni":"s.com","net":"ws","host":"cdn.example.com","path":"/ws","flow":"xtls-rprx-vision"}`}},
		{Node: storage.Node{Name: "vm", Address: "vm.example.com", Port: 8443, Protocol: "vmess",
			Config: `{"uuid":"u-2","scy":"aes-128-gcm","aid":64,"tls":true,"sni":"s.jp","net":"grpc","path":"srv"}`}},
		{Node: storage.Node{Name: "tj", Address: "tj.example.com", Port: 443, Protocol: "trojan",
			Config: `{"password":"pw","sni":"s.com"}`}},
		{Node: storage.Node{Name: "ss", Address: "ss.example.com", Port: 8388, Protocol: "shadowsocks",
			Config: `{"method":"aes-256-gcm","password":"pw"}`}},
	}
	obs := mustXrayOutbounds(t, entries)
	if len(obs) != 4 {
		t.Fatalf("outbounds = %d, want 4", len(obs))
	}

	// vless: settings.vnext + the ws transport settings.
	ob := obs[0]
	if ob["tag"] != "vl" || ob["protocol"] != "vless" {
		t.Fatalf("vless head = %v", ob)
	}
	stream := xrayStreamOf(t, ob)
	user := xrayUser(t, ob, "vless", "vl.example.com", 443)
	if user["id"] != "u-1" || user["encryption"] != "none" || user["flow"] != "xtls-rprx-vision" {
		t.Fatalf("vless user = %v", user)
	}
	if stream["network"] != "ws" || stream["security"] != "tls" {
		t.Fatalf("vless stream = %v", stream)
	}
	ws, ok := stream["wsSettings"].(map[string]any)
	if !ok || ws["path"] != "/ws" {
		t.Fatalf("vless wsSettings = %v", stream["wsSettings"])
	}
	h, ok := ws["headers"].(map[string]any)
	if !ok || h["Host"] != "cdn.example.com" {
		t.Fatalf("vless ws headers = %v", ws)
	}
	if tls := tlsSettings(t, stream); tls["serverName"] != "s.com" {
		t.Fatalf("vless tlsSettings = %v", stream["tlsSettings"])
	}

	// vmess: scy passes through, grpc uses serviceName.
	vm := obs[1]
	if vm["tag"] != "vm" || vm["protocol"] != "vmess" {
		t.Fatalf("vmess head = %v", vm)
	}
	user = xrayUser(t, vm, "vmess", "vm.example.com", 8443)
	if user["id"] != "u-2" || user["security"] != "aes-128-gcm" || user["alterId"] != float64(64) {
		t.Fatalf("vmess user = %v", user)
	}
	stream = xrayStreamOf(t, vm)
	if stream["network"] != "grpc" || stream["security"] != "tls" {
		t.Fatalf("vmess stream = %v", stream)
	}
	grpc, ok := stream["grpcSettings"].(map[string]any)
	if !ok || grpc["serviceName"] != "srv" {
		t.Fatalf("vmess grpcSettings = %v", stream["grpcSettings"])
	}

	// trojan: always TLS (the config writes no tls key), password inside servers.
	tj := obs[2]
	if tj["tag"] != "tj" || tj["protocol"] != "trojan" {
		t.Fatalf("trojan head = %v", tj)
	}
	if srv := xrayServers(t, tj, "trojan", "tj.example.com", 443); srv["password"] != "pw" {
		t.Fatalf("trojan server = %v", srv)
	}
	stream = xrayStreamOf(t, tj)
	if stream["security"] != "tls" || stream["network"] != "tcp" {
		t.Fatalf("trojan must be tls over tcp: %v", stream)
	}
	if tls := tlsSettings(t, stream); tls["serverName"] != "s.com" {
		t.Fatalf("trojan tlsSettings = %v", tls)
	}

	// shadowsocks: method/password inside servers, and no streamSettings.
	ss := obs[3]
	if ss["tag"] != "ss" || ss["protocol"] != "shadowsocks" {
		t.Fatalf("ss head = %v", ss)
	}
	if _, ok := ss["streamSettings"]; ok {
		t.Fatalf("ss must not have streamSettings: %v", ss)
	}
	srv := xrayServers(t, ss, "shadowsocks", "ss.example.com", 8388)
	if srv["method"] != "aes-256-gcm" || srv["password"] != "pw" {
		t.Fatalf("ss server = %v", srv)
	}
}

// TestXrayOutboundVlessMinimal covers the defaults: the tcp network, no TLS
// object, and the serverName fallback to the node address.
func TestXrayOutboundVlessMinimal(t *testing.T) {
	obs := mustXrayOutbounds(t, []Entry{
		{Node: storage.Node{Name: "vl", Address: "a", Port: 1, Protocol: "vless", Config: `{"uuid":"u"}`}},
	})
	ob := obs[0]
	user := xrayUser(t, ob, "vless", "a", 1)
	if user["id"] != "u" || user["encryption"] != "none" {
		t.Fatalf("vless user = %v", user)
	}
	if _, ok := user["flow"]; ok {
		t.Fatalf("vless min must not have flow: %v", user)
	}
	stream := xrayStreamOf(t, ob)
	if stream["network"] != "tcp" {
		t.Fatalf("vless min network = %v", stream)
	}
	for _, k := range []string{"security", "tlsSettings", "wsSettings"} {
		if _, ok := stream[k]; ok {
			t.Fatalf("vless min stream has %s: %v", k, stream)
		}
	}
	// An sni-less TLS node falls back to the node address as serverName.
	obs = mustXrayOutbounds(t, []Entry{
		{Node: storage.Node{Name: "vl", Address: "a.example.com", Port: 443, Protocol: "vless",
			Config: `{"uuid":"u","tls":true,"insecure":true}`}},
	})
	tls := tlsSettings(t, xrayStreamOf(t, obs[0]))
	if tls["serverName"] != "a.example.com" || tls["allowInsecure"] != true {
		t.Fatalf("vless sni fallback = %v", tls)
	}
}

// TestXrayOutboundNets covers the remaining transports and the explicit error for
// an unknown net (Xray has no passthrough, same convention as sing-box).
func TestXrayOutboundNets(t *testing.T) {
	if _, err := XrayOutboundOf(&storage.Node{Name: "x", Protocol: "vless", Config: `{"uuid":"u","net":"kcp"}`}); err == nil || !strings.Contains(err.Error(), "kcp") {
		t.Fatalf("unknown net err = %v", err)
	}
	for _, net := range []string{"http", "httpupgrade"} {
		ob, err := XrayOutboundOf(&storage.Node{Name: "x", Address: "a", Port: 1, Protocol: "vless",
			Config: `{"uuid":"u","net":"` + net + `","host":"h.com","path":"/p"}`})
		if err != nil {
			t.Fatalf("net %s: %v", net, err)
		}
		stream := xrayStreamOf(t, ob)
		if stream["network"] != net {
			t.Fatalf("net %s network = %v", net, stream)
		}
		settings, ok := stream[net+"Settings"].(map[string]any)
		if !ok || settings["path"] != "/p" {
			t.Fatalf("net %s settings = %v", net, stream[net+"Settings"])
		}
	}
}

// TestPackXraySkipsUnsupported covers the skip semantics: a fleet mixing an
// xray-family protocol with the protocols Xray has no outbound for packs the
// former only, without failing the whole pack.
func TestPackXraySkipsUnsupported(t *testing.T) {
	entries := []Entry{
		{Node: storage.Node{Name: "vl", Address: "a", Port: 1, Protocol: "vless", Config: `{"uuid":"u1"}`}},
		{Node: storage.Node{Name: "hy", Address: "b", Port: 2, Protocol: "hysteria2", Config: `{"password":"p"}`}},
		{Node: storage.Node{Name: "tu", Address: "c", Port: 3, Protocol: "tuic", Config: `{"uuid":"u","password":"p"}`}},
		{Node: storage.Node{Name: "wg", Address: "d", Port: 4, Protocol: "wireguard", Config: `{}`}},
	}
	obs := mustXrayOutbounds(t, entries)
	if len(obs) != 1 {
		t.Fatalf("outbounds = %d, want 1", len(obs))
	}
	if obs[0]["protocol"] != "vless" || obs[0]["tag"] != "vl" {
		t.Fatalf("outbound[0] = %v", obs[0])
	}
}

// TestXrayErrors covers the failure surfaces: an unknown protocol name, a missing
// required field, and the single-point conversion of a protocol without a RenderXray
// hook (no skip semantics there).
func TestXrayErrors(t *testing.T) {
	if _, err := PackXray([]Entry{{Node: storage.Node{Name: "x", Protocol: "ssr", Config: `{}`}}}); err == nil || !strings.Contains(err.Error(), "ssr") {
		t.Fatalf("unknown protocol err = %v", err)
	}
	if _, err := PackXray([]Entry{{Node: storage.Node{Name: "x", Protocol: "vless", Config: `{}`}}}); err == nil || !strings.Contains(err.Error(), "vless requires config.uuid") {
		t.Fatalf("missing uuid err = %v", err)
	}
	if _, err := XrayOutboundOf(&storage.Node{Name: "x", Protocol: "hysteria2", Config: `{"password":"p"}`}); err == nil || !strings.Contains(err.Error(), "hysteria2") {
		t.Fatalf("no xray outbound err = %v", err)
	}
}

// mustXrayOutbounds unwraps a PackXray output to make per-field assertions easier.
func mustXrayOutbounds(t *testing.T, entries []Entry) []map[string]any {
	t.Helper()
	out, err := PackXray(entries)
	if err != nil {
		t.Fatalf("PackXray: %v", err)
	}
	var obs []map[string]any
	if err := json.Unmarshal([]byte(out), &obs); err != nil {
		t.Fatalf("unmarshal outbounds: %v\n%s", err, out)
	}
	return obs
}

// xrayStreamOf unwraps the outbound's streamSettings (required for the assertions).
func xrayStreamOf(t *testing.T, ob map[string]any) map[string]any {
	t.Helper()
	stream, ok := ob["streamSettings"].(map[string]any)
	if !ok {
		t.Fatalf("streamSettings = %v", ob["streamSettings"])
	}
	return stream
}

// xrayUser unwraps the single user object of a vnext-based outbound and asserts
// the address / port it sits under.
func xrayUser(t *testing.T, ob map[string]any, proto, address string, port int) map[string]any {
	t.Helper()
	settings, ok := ob["settings"].(map[string]any)
	if !ok {
		t.Fatalf("%s settings = %v", proto, ob["settings"])
	}
	vnext := settings["vnext"].([]any)
	entry := vnext[0].(map[string]any)
	if entry["address"] != address || entry["port"] != float64(port) {
		t.Fatalf("%s vnext = %v", proto, entry)
	}
	users := entry["users"].([]any)
	return users[0].(map[string]any)
}

// xrayServers unwraps the single server object of a servers-based outbound and
// asserts the address / port.
func xrayServers(t *testing.T, ob map[string]any, proto, address string, port int) map[string]any {
	t.Helper()
	settings, ok := ob["settings"].(map[string]any)
	if !ok {
		t.Fatalf("%s settings = %v", proto, ob["settings"])
	}
	servers := settings["servers"].([]any)
	srv := servers[0].(map[string]any)
	if srv["address"] != address || srv["port"] != float64(port) {
		t.Fatalf("%s servers = %v", proto, srv)
	}
	return srv
}

// tlsSettings unwraps the stream's tlsSettings object.
func tlsSettings(t *testing.T, stream map[string]any) map[string]any {
	t.Helper()
	tls, ok := stream["tlsSettings"].(map[string]any)
	if !ok {
		t.Fatalf("tlsSettings = %v", stream["tlsSettings"])
	}
	return tls
}
