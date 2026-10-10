package sub

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

func TestShareLink(t *testing.T) {
	cases := []struct {
		name string
		node storage.Node
		want string
	}{
		{
			name: "vless 全字段",
			node: storage.Node{Name: "香港 01", Address: "hk.example.com", Port: 443, Protocol: "vless",
				Config: `{"uuid":"u-1","tls":true,"sni":"sni.example.com","net":"ws","host":"cdn.example.com","path":"/ws","flow":"xtls-rprx-vision"}`},
			want: "vless://u-1@hk.example.com:443?encryption=none&flow=xtls-rprx-vision&host=cdn.example.com&path=%2Fws&security=tls&sni=sni.example.com&type=ws#%E9%A6%99%E6%B8%AF%2001",
		},
		{
			name: "vless 无配置仅补 encryption",
			node: storage.Node{Name: "n", Address: "a", Port: 1, Protocol: "vless", Config: `{"uuid":"uid"}`},
			want: "vless://uid@a:1?encryption=none#n",
		},
		{
			name: "trojan",
			node: storage.Node{Name: "t", Address: "a", Port: 443, Protocol: "trojan",
				Config: `{"password":"pw","tls":true,"sni":"s.com","net":"grpc"}`},
			want: "trojan://pw@a:443?security=tls&sni=s.com&type=grpc#t",
		},
		{
			name: "shadowsocks",
			node: storage.Node{Name: "n", Address: "a", Port: 8388, Protocol: "shadowsocks",
				Config: `{"method":"aes-256-gcm","password":"pw"}`},
			want: "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:pw")) + "@a:8388#n",
		},
		{
			name: "hysteria2 全字段",
			node: storage.Node{Name: "hy", Address: "hy.example.com", Port: 443, Protocol: "hysteria2",
				Config: `{"password":"pw","sni":"s.com","obfs":"salamander","obfs_password":"op","up":100,"down":200}`},
			want: "hysteria2://pw@hy.example.com:443?down=200&obfs=salamander&obfs-password=op&sni=s.com&up=100#hy",
		},
		{
			name: "hysteria2 仅密码+自签",
			node: storage.Node{Name: "hy2", Address: "a", Port: 443, Protocol: "hysteria2",
				Config: `{"password":"pw","insecure":true}`},
			want: "hysteria2://pw@a:443?insecure=1#hy2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShareLink(&tc.node)
			if err != nil {
				t.Fatalf("ShareLink: %v", err)
			}
			if got != tc.want {
				t.Fatalf("link mismatch:\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestShareLinkVmess(t *testing.T) {
	n := storage.Node{Name: "vm", Address: "a", Port: 443, Protocol: "vmess",
		Config: `{"uuid":"uid","tls":true,"sni":"s.com","net":"ws","path":"/w","host":"h.com"}`}
	link, err := ShareLink(&n)
	if err != nil {
		t.Fatalf("ShareLink: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatalf("decode vmess payload: %v", err)
	}
	var obj map[string]string
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal vmess payload: %v", err)
	}
	for k, want := range map[string]string{
		"v": "2", "ps": "vm", "add": "a", "port": "443", "id": "uid",
		"aid": "0", "scy": "auto", "net": "ws", "type": "none",
		"host": "h.com", "path": "/w", "tls": "tls", "sni": "s.com",
	} {
		if obj[k] != want {
			t.Fatalf("vmess[%s] = %q, want %q", k, obj[k], want)
		}
	}
}

func TestShareLinkErrors(t *testing.T) {
	cases := []struct {
		name string
		node storage.Node
	}{
		{"协议不支持", storage.Node{Protocol: "socks", Config: `{}`}},
		{"vless 缺 uuid", storage.Node{Protocol: "vless", Config: `{}`}},
		{"vmess 缺 uuid", storage.Node{Protocol: "vmess", Config: `{}`}},
		{"trojan 缺密码", storage.Node{Protocol: "trojan", Config: `{}`}},
		{"ss 缺 method", storage.Node{Protocol: "shadowsocks", Config: `{"password":"pw"}`}},
		{"hysteria2 缺密码", storage.Node{Protocol: "hysteria2", Config: `{"sni":"s.com"}`}},
		{"配置非 JSON", storage.Node{Protocol: "vless", Config: `{"uuid":`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ShareLink(&tc.node); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestPackV2Ray(t *testing.T) {
	nodes := []storage.Node{
		{Name: "a", Address: "a", Port: 1, Protocol: "vless", Config: `{"uuid":"u1"}`},
		{Name: "b", Address: "b", Port: 2, Protocol: "vless", Config: `{"uuid":"u2"}`},
	}
	entries := []Entry{{Node: nodes[0]}, {Node: nodes[1]}}
	packed, err := PackV2Ray(entries, nil)
	if err != nil {
		t.Fatalf("PackV2Ray: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(packed)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "vless://u1@a:1") || !strings.HasPrefix(lines[1], "vless://u2@b:2") {
		t.Fatalf("unexpected payload: %q", string(raw))
	}

	// 空列表返回空订阅（无可用节点仍可刷新）。
	if empty, err := PackV2Ray(nil, nil); err != nil || empty != "" {
		t.Fatalf("empty pack = %q, %v", empty, err)
	}
}
