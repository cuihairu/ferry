package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPackSingBox 覆盖 sing-box outbounds 数组生成（hy2 批）：
// hysteria2 出站字段 + 最小配置省略可选键 + 不支持协议报错。
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
	// obfs 是嵌套对象 {type, password}。
	if obfs, ok := ob["obfs"].(map[string]any); !ok || obfs["type"] != "salamander" || obfs["password"] != "op" {
		t.Fatalf("outbound[0] obfs = %v", ob["obfs"])
	}
	// 最小配置：无可选键。
	m := obs[1]
	for _, k := range []string{"sni", "insecure", "obfs", "up_mbps", "down_mbps"} {
		if _, ok := m[k]; ok {
			t.Fatalf("outbound[1] has %s: %v", k, m)
		}
	}

	// 缺密码报错（与 ShareLink 同口径，错误带节点名）。
	if _, err := PackSingBox([]Entry{{Node: storage.Node{Name: "bad", Protocol: "hysteria2", Config: `{}`}}}, ); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("missing password err = %v", err)
	}
	// 非法 JSON 配置报错透传。
	if _, err := PackSingBox([]Entry{{Node: storage.Node{Name: "x", Protocol: "hysteria2", Config: `{"password":`}}}); err == nil {
		t.Fatal("expected config parse error")
	}
}
