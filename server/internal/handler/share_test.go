package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// TestNodeShare 覆盖单节点分享链接（P1-9）：四种协议各构造一次 +
// 未知协议 400 + 不存在 404。
func TestNodeShare(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	cases := []struct {
		name     string
		protocol string
		config   string
		prefix   string
		contains string
	}{
		{"vless", "vless", `{"uuid":"u-1","tls":true,"sni":"s.com","net":"ws","path":"/wss","host":"cdn.example.com"}`,
			"vless://u-1@a.example.com:443?", "encryption=none"},
		{"vmess", "vmess", `{"uuid":"u-2","tls":true,"net":"ws"}`,
			"vmess://", ""},
		{"trojan", "trojan", `{"password":"pw-1"}`,
			"trojan://pw-1@a.example.com:443?", ""},
		{"shadowsocks", "shadowsocks", `{"method":"aes-128-gcm","password":"pw-2"}`,
			"ss://", "@a.example.com:443#"},
	}
	for i, tc := range cases {
		rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": tc.name, "address": "a.example.com", "port": 443,
			"protocol": tc.protocol, "config": tc.config,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s node: %d %s", tc.protocol, rec.Code, rec.Body)
		}
		rec = doJSON(t, r, "GET", "/api/nodes/"+strconv.Itoa(i+1)+"/share", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("share %s: %d %s", tc.protocol, rec.Code, rec.Body)
		}
		var out struct {
			NodeID uint   `json:"node_id"`
			Name   string `json:"name"`
			Link   string `json:"link"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Name != tc.name || !strings.HasPrefix(out.Link, tc.prefix) || !strings.Contains(out.Link, tc.contains) {
			t.Fatalf("%s share mismatch: %+v", tc.protocol, out)
		}
	}

	// 未知协议 400（创建端有协议白名单，直改库构造存量脏数据场景）
	if err := db.Exec(`UPDATE nodes SET protocol='wireguard' WHERE name='vless'`).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "GET", "/api/nodes/1/share", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("unsupported protocol should 400: %d %s", rec.Code, rec.Body)
	}

	// 不存在 404
	if rec := doJSON(t, r, "GET", "/api/nodes/999/share", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing node should 404: %d", rec.Code)
	}
}
