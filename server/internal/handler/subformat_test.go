package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// g1Node creates one entry-role node per protocol (the subscription only delivers
// entry/both-role nodes, the same convention as TestSubscription).
func g1Node(t *testing.T, r *gin.Engine, db *gorm.DB, name, protocol, config string) {
	t.Helper()
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": name, "address": name + ".example.com", "port": 443,
		"protocol": protocol, "config": config,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node %s: %d %s", name, rec.Code, rec.Body)
	}
	if err := db.Model(&storage.Node{}).Where("name = ?", name).
		Updates(map[string]any{"role": "entry", "meta_init": true}).Error; err != nil {
		t.Fatalf("set role %s: %v", name, err)
	}
}

// TestSubscriptionFormatsG1 covers the five subscription formats and the UA
// negotiation (G1): mihomo (Clash.Meta) carries the meta extensions, the plain
// clash target keeps its own vocabulary, xray-json parses back into outbounds,
// and an unknown UA still falls back to the v2ray base64 links.
func TestSubscriptionFormatsG1(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "g1", "quota_bytes": 1 << 20, "expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	subPath := "/sub/" + u["sub_token"].(string)

	g1Node(t, r, db, "hk-vless", "vless", `{"uuid":"u-1","tls":true,"sni":"s.example.com","net":"ws","path":"/wss","host":"cdn.example.com"}`)
	g1Node(t, r, db, "us-vmess", "vmess", `{"uuid":"v-2","tls":true,"sni":"s.example.com"}`)
	g1Node(t, r, db, "jp-trojan", "trojan", `{"password":"pw-3","tls":true,"sni":"s.example.com"}`)
	g1Node(t, r, db, "de-ss", "shadowsocks", `{"method":"aes-256-gcm","password":"ss-4"}`)
	g1Node(t, r, db, "sg-hy2", "hysteria2", `{"password":"hy-5"}`)

	// mihomo (Clash.Meta) target: meta extensions only
	mihomo := getSub(t, r, subPath+"?target=mihomo", "curl/8.0")
	if mihomo.Code != http.StatusOK || !strings.Contains(mihomo.Header().Get("Content-Type"), "text/yaml") {
		t.Fatalf("mihomo target: %d %v", mihomo.Code, mihomo.Header().Get("Content-Type"))
	}
	if b := mihomo.Body.String(); !strings.Contains(b, "proxies:") || !strings.Contains(b, "packet-encoding: xudp") || !strings.Contains(b, "lazy: true") {
		t.Fatalf("mihomo body incomplete:\n%s", b)
	}

	// The plain clash target must not pick up the meta extensions (the legacy
	// output stays byte-compatible, golden-gated).
	clash := getSub(t, r, subPath+"?target=clash", "curl/8.0")
	if b := clash.Body.String(); !strings.Contains(b, "proxies:") || strings.Contains(b, "packet-encoding") || strings.Contains(b, "lazy:") {
		t.Fatalf("clash body must stay meta-free:\n%s", b)
	}

	// xray-json target: parses back into an Xray outbounds array
	xray := getSub(t, r, subPath+"?target=xray", "curl/8.0")
	if xray.Code != http.StatusOK || !strings.Contains(xray.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("xray target: %d %v", xray.Code, xray.Header().Get("Content-Type"))
	}
	var outbounds []map[string]any
	if err := json.Unmarshal(xray.Body.Bytes(), &outbounds); err != nil {
		t.Fatalf("xray body not JSON: %v (%s)", err, xray.Body)
	}
	// hy2 has no Xray outbound: four of the five nodes survive
	if len(outbounds) != 4 {
		t.Fatalf("xray outbounds = %d: %s", len(outbounds), xray.Body)
	}
	byProto := map[string]map[string]any{}
	for _, ob := range outbounds {
		byProto[ob["protocol"].(string)] = ob
	}
	vless := byProto["vless"]
	nxt := vless["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)
	user := nxt["users"].([]any)[0].(map[string]any)
	stream := vless["streamSettings"].(map[string]any)
	if nxt["address"] != "hk-vless.example.com" || nxt["port"] != float64(443) ||
		user["id"] != "u-1" || user["encryption"] != "none" ||
		stream["network"] != "ws" || stream["security"] != "tls" ||
		stream["wsSettings"].(map[string]any)["path"] != "/wss" ||
		stream["tlsSettings"].(map[string]any)["serverName"] != "s.example.com" ||
		stream["wsSettings"].(map[string]any)["headers"].(map[string]any)["Host"] != "cdn.example.com" {
		t.Fatalf("vless outbound = %v", vless)
	}
	if vm := byProto["vmess"]; vm["tag"] != "us-vmess" {
		t.Fatalf("vmess outbound = %v", vm)
	}
	if tp := byProto["trojan"]; tp["settings"].(map[string]any)["servers"].([]any)[0].(map[string]any)["password"] != "pw-3" {
		t.Fatalf("trojan outbound = %v", tp)
	}
	if ss := byProto["shadowsocks"]; ss["settings"].(map[string]any)["servers"].([]any)[0].(map[string]any)["method"] != "aes-256-gcm" {
		t.Fatalf("shadowsocks outbound = %v", ss)
	}
	if strings.Contains(xray.Body.String(), "hysteria2") {
		t.Fatalf("hy2 leaked into xray-json: %s", xray.Body)
	}

	// UA negotiation: meta family, plain clash, sing-box, xray, unknown fallback
	cases := []struct {
		ua      string
		wantHas []string
		wantNot []string
	}{
		{"ClashMetaForAndroid/2.10.03", []string{"packet-encoding: xudp"}, []string{}},
		{"clash-verge/2.0", []string{"packet-encoding: xudp"}, []string{}},
		{"mihomo/1.18.0", []string{"lazy: true"}, []string{}},
		{"clash/0.20.0", []string{"proxies:"}, []string{"packet-encoding", "lazy:"}},
		{"sing-box/1.9", []string{`"type": "vless"`}, []string{"proxies:"}},
		{"xray/1.8.24", []string{`"protocol": "vless"`}, []string{"proxies:"}},
	}
	for _, c := range cases {
		rec := getSub(t, r, subPath, c.ua)
		if rec.Code != http.StatusOK {
			t.Fatalf("sub %q: %d", c.ua, rec.Code)
		}
		body := rec.Body.String()
		for _, want := range c.wantHas {
			if !strings.Contains(body, want) {
				t.Fatalf("sub %q misses %q:\n%s", c.ua, want, body)
			}
		}
		for _, absent := range c.wantNot {
			if strings.Contains(body, absent) {
				t.Fatalf("sub %q must not contain %q:\n%s", c.ua, absent, body)
			}
		}
	}

	// Unknown UAs fall back to the v2ray base64 links
	for _, ua := range []string{"curl/8.0", "v2rayNG/1.8"} {
		rec := getSub(t, r, subPath, ua)
		if rec.Code != http.StatusOK {
			t.Fatalf("sub %q: %d", ua, rec.Code)
		}
		if _, err := base64.StdEncoding.DecodeString(rec.Body.String()); err != nil {
			t.Fatalf("sub %q should fall back to base64 links: %v", ua, err)
		}
	}
}

// TestProtocolsFormatsG1 covers the format facet of GET /api/protocols: the
// mihomo format follows the clash render hook, xray-json only the protocols an
// Xray outbound exists for.
func TestProtocolsFormatsG1(t *testing.T) {
	r := newTestRouter(t)
	rec := doJSON(t, r, "GET", "/api/protocols", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("protocols: %d %s", rec.Code, rec.Body)
	}
	var list []struct {
		Name    string          `json:"name"`
		Formats map[string]bool `json:"formats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode protocols: %v", err)
	}
	byName := map[string]map[string]bool{}
	for _, p := range list {
		byName[p.Name] = p.Formats
	}
	if !byName["vless"]["mihomo"] || !byName["vless"]["xray"] || !byName["vless"]["clash"] {
		t.Fatalf("vless formats = %v", byName["vless"])
	}
	if byName["hysteria2"]["xray"] || !byName["hysteria2"]["mihomo"] {
		t.Fatalf("hysteria2 formats = %v", byName["hysteria2"])
	}
}
