package sub

import (
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestClashProxyOfValidationErrors 各协议缺必填字段/配置非 JSON 的拒绝面。
func TestClashProxyOfValidationErrors(t *testing.T) {
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
		{"ss no password", "shadowsocks", `{"method":"aes-128-gcm"}`, "shadowsocks requires config.method"},
		{"hysteria2 no password", "hysteria2", `{}`, "hysteria2 requires config.password"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := clashProxyOf(&storage.Node{Name: "n", Address: "a", Port: 443,
				Protocol: c.protocol, Config: c.config})
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err = %v; want contains %q", err, c.wantErr)
			}
		})
	}
}
