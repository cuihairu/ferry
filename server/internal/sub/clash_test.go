package sub

import (
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gopkg.in/yaml.v3"
)

func TestPackClash(t *testing.T) {
	entries := []Entry{
		{Node: storage.Node{Name: "hk", Address: "hk.example.com", Port: 443, Protocol: "vless", Region: "香港",
			Config: `{"uuid":"u-1","tls":true,"sni":"sni.example.com","net":"ws","host":"cdn.example.com","path":"/ws"}`}},
		{Node: storage.Node{Name: "ss", Address: "ss.example.com", Port: 8388, Protocol: "shadowsocks",
			Config: `{"method":"aes-256-gcm","password":"pw"}`}},
		// 与第一个重名：clash 的 name 是唯一键，必须改名输出
		{Node: storage.Node{Name: "hk", Address: "hk2.example.com", Port: 443, Protocol: "vless", Region: "香港", Config: `{"uuid":"u-2"}`}},
	}
	out, err := PackClash(entries, nil)
	if err != nil {
		t.Fatalf("PackClash: %v", err)
	}

	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
		Groups  []struct {
			Name     string   `yaml:"name"`
			Type     string   `yaml:"type"`
			Proxies  []string `yaml:"proxies"`
			URL      string   `yaml:"url"`
			Interval int      `yaml:"interval"`
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

	// E-19：PROXY select 聚合区域组；香港 url-test 组收 hk/hk-2，空区域归「未知」
	if len(doc.Groups) != 3 || doc.Groups[0].Name != "PROXY" || doc.Groups[0].Type != "select" {
		t.Fatalf("groups = %+v", doc.Groups)
	}
	if doc.Groups[0].Proxies[0] != "香港" || doc.Groups[0].Proxies[1] != "未知" {
		t.Fatalf("PROXY group = %v", doc.Groups[0].Proxies)
	}
	hk := doc.Groups[1]
	if hk.Name != "香港" || hk.Type != "url-test" {
		t.Fatalf("hk group = %+v", hk)
	}
	if len(hk.Proxies) != 2 || hk.Proxies[1] != "hk-2" {
		t.Fatalf("hk proxies = %v", hk.Proxies)
	}
	if hk.URL == "" || hk.Interval == 0 {
		t.Fatalf("url-test url/interval missing: %+v", hk)
	}
	unknown := doc.Groups[2]
	if unknown.Name != "未知" || len(unknown.Proxies) != 1 || unknown.Proxies[0] != "ss" {
		t.Fatalf("unknown group = %+v", unknown)
	}
	if len(doc.Rules) != 1 || doc.Rules[0] != "MATCH,PROXY" {
		t.Fatalf("rules = %v", doc.Rules)
	}
}

func TestPackClashNameCollisions(t *testing.T) {
	// 节点名与区域组名同名：代理与组共用命名空间，后者追加序号保证唯一
	entries := []Entry{
		{Node: storage.Node{Name: "PROXY", Address: "a", Port: 1, Protocol: "vless", Config: `{"uuid":"u1"}`, Region: "香港"}},
		{Node: storage.Node{Name: "香港", Address: "b", Port: 2, Protocol: "vless", Config: `{"uuid":"u2"}`, Region: "美国"}},
	}
	out, err := PackClash(entries, nil)
	if err != nil {
		t.Fatalf("PackClash: %v", err)
	}
	// PROXY 被顶走、香港被顶走；引用必须自洽（组引用的名字都存在）
	if !strings.Contains(out, "name: PROXY-2") || !strings.Contains(out, "name: 香港-2") {
		t.Fatalf("collision rename missing:\n%s", out)
	}
	if strings.Count(out, "name: 香港\n") != 1 { // 只剩区域组本身
		t.Fatalf("香港 name not unique:\n%s", out)
	}
}

func TestPackClashVmessAndTrojan(t *testing.T) {
	entries := []Entry{
		{Node: storage.Node{Name: "vm", Address: "a", Port: 443, Protocol: "vmess",
			Config: `{"uuid":"u-1","scy":"aes-128-gcm","net":"ws","path":"/w"}`}},
		{Node: storage.Node{Name: "tj", Address: "b", Port: 443, Protocol: "trojan",
			Config: `{"password":"pw","sni":"s.com"}`}},
	}
	out, err := PackClash(entries, nil)
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

// TestPackClashHysteria2 覆盖 hy2 节点在 mihomo/clash 里的条目（hy2 批）：
// type: hysteria2 + password/up/down/obfs/obfs-password/sni/skip-cert-verify。
func TestPackClashHysteria2(t *testing.T) {
	entries := []Entry{
		{Node: storage.Node{Name: "hy", Address: "hy.example.com", Port: 443, Protocol: "hysteria2",
			Config: `{"password":"pw","sni":"s.com","obfs":"salamander","obfs_password":"op","up":100,"down":200,"insecure":true}`}},
		{Node: storage.Node{Name: "hy-min", Address: "b", Port: 8443, Protocol: "hysteria2",
			Config: `{"password":"pw2"}`}},
	}
	out, err := PackClash(entries, nil)
	if err != nil {
		t.Fatalf("PackClash: %v", err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("unmarshal yaml: %v\n%s", err, out)
	}
	p := doc.Proxies[0]
	if p["type"] != "hysteria2" || p["password"] != "pw" || p["sni"] != "s.com" {
		t.Fatalf("hy2 proxy = %v", p)
	}
	if p["obfs"] != "salamander" || p["obfs-password"] != "op" {
		t.Fatalf("hy2 obfs = %v", p)
	}
	// yaml 会把数字解成 int，直接比较 any 需按数值口径。
	if p["up"] != int(100) || p["down"] != int(200) {
		t.Fatalf("hy2 up/down = %v", p)
	}
	if p["skip-cert-verify"] != true {
		t.Fatalf("hy2 skip-cert-verify = %v", p)
	}
	// 最小配置：只出 password，无 obfs/带宽/自签字段。
	m := doc.Proxies[1]
	if m["type"] != "hysteria2" || m["password"] != "pw2" {
		t.Fatalf("hy2 min proxy = %v", m)
	}
	for _, k := range []string{"obfs", "obfs-password", "up", "down", "skip-cert-verify"} {
		if _, ok := m[k]; ok {
			t.Fatalf("hy2 min proxy has %s: %v", k, m)
		}
	}
	if _, err := PackClash([]Entry{{Node: storage.Node{Name: "x", Protocol: "hysteria2", Config: `{}`}}}, nil); err == nil {
		t.Fatal("expected error for hysteria2 missing password")
	}
}

func TestPackClashErrors(t *testing.T) {
	if _, err := PackClash([]Entry{{Node: storage.Node{Name: "x", Protocol: "socks"}}}, nil); err == nil {
		t.Fatal("expected error for unsupported protocol")
	}
}
