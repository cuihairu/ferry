package sub

import (
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gopkg.in/yaml.v3"
)

func TestPackClash(t *testing.T) {
	nodes := []storage.Node{
		{Name: "hk", Address: "hk.example.com", Port: 443, Protocol: "vless",
			Config: `{"uuid":"u-1","tls":true,"sni":"sni.example.com","net":"ws","host":"cdn.example.com","path":"/ws"}`},
		{Name: "ss", Address: "ss.example.com", Port: 8388, Protocol: "shadowsocks",
			Config: `{"method":"aes-256-gcm","password":"pw"}`},
		// 与第一个重名：clash 的 name 是唯一键，必须改名输出
		{Name: "hk", Address: "hk2.example.com", Port: 443, Protocol: "vless", Config: `{"uuid":"u-2"}`},
	}
	out, err := PackClash(nodes)
	if err != nil {
		t.Fatalf("PackClash: %v", err)
	}

	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
		Groups  []struct {
			Name    string   `yaml:"name"`
			Type    string   `yaml:"type"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal yaml: %v\n%s", err, out)
	}
	if len(doc.Proxies) != 3 {
		t.Fatalf("proxies = %d, want 3:\n%s", len(doc.Proxies), out)
	}
	p0 := doc.Proxies[0]
	if p0["type"] != "vless" || p0["uuid"] != "u-1" || p0["servername"] != "sni.example.com" {
		t.Fatalf("proxy[0] = %v", p0)
	}
	ws, ok := p0["ws-opts"].(map[string]any)
	if !ok || ws["path"] != "/ws" {
		t.Fatalf("proxy[0] ws-opts = %v", p0["ws-opts"])
	}
	headers, ok := ws["headers"].(map[string]any)
	if !ok || headers["Host"] != "cdn.example.com" {
		t.Fatalf("proxy[0] ws headers = %v", ws["headers"])
	}
	p1 := doc.Proxies[1]
	if p1["type"] != "ss" || p1["cipher"] != "aes-256-gcm" || p1["password"] != "pw" {
		t.Fatalf("proxy[1] = %v", p1)
	}
	if name, _ := doc.Proxies[2]["name"].(string); name != "hk-2" {
		t.Fatalf("duplicate name not renamed: %v", doc.Proxies[2]["name"])
	}
	if len(doc.Groups) != 1 || doc.Groups[0].Name != "PROXY" {
		t.Fatalf("groups = %v", doc.Groups)
	}
	if len(doc.Groups[0].Proxies) != 3 || doc.Groups[0].Proxies[2] != "hk-2" {
		t.Fatalf("group proxies = %v", doc.Groups[0].Proxies)
	}
	if len(doc.Rules) != 1 || doc.Rules[0] != "MATCH,PROXY" {
		t.Fatalf("rules = %v", doc.Rules)
	}
}

func TestPackClashVmessAndTrojan(t *testing.T) {
	nodes := []storage.Node{
		{Name: "vm", Address: "a", Port: 443, Protocol: "vmess",
			Config: `{"uuid":"u-1","scy":"aes-128-gcm","net":"ws","path":"/w"}`},
		{Name: "tj", Address: "b", Port: 443, Protocol: "trojan",
			Config: `{"password":"pw","sni":"s.com"}`},
	}
	out, err := PackClash(nodes)
	if err != nil {
		t.Fatalf("PackClash: %v", err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal yaml: %v\n%s", err, out)
	}
	if doc.Proxies[0]["cipher"] != "aes-128-gcm" || doc.Proxies[0]["network"] != "ws" {
		t.Fatalf("vmess proxy = %v", doc.Proxies[0])
	}
	if doc.Proxies[1]["sni"] != "s.com" || doc.Proxies[1]["password"] != "pw" {
		t.Fatalf("trojan proxy = %v", doc.Proxies[1])
	}
}

func TestPackClashErrors(t *testing.T) {
	if _, err := PackClash([]storage.Node{{Name: "x", Protocol: "socks"}}); err == nil {
		t.Fatal("expected error for unsupported protocol")
	}
}
