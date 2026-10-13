package model

import "testing"

func TestValidProtocol(t *testing.T) {
	valid := []string{ProtoVless, ProtoVmess, ProtoTrojan, ProtoShadowsocks, ProtoHysteria2}
	for _, p := range valid {
		if !ValidProtocol(p) {
			t.Errorf("ValidProtocol(%q) = false; want true", p)
		}
	}
	invalid := []string{"", "ss", "vmess ", "VLESS", "HY2", "anytls", "reality"}
	for _, p := range invalid {
		if ValidProtocol(p) {
			t.Errorf("ValidProtocol(%q) = true; want false", p)
		}
	}
}
