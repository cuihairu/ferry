package sub

import (
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSingBoxHysteria2ObfsPassword hysteria2 混淆口令完整展开：obfs 对象
// 带 type 与 password，主密码在顶层。
func TestSingBoxHysteria2ObfsPassword(t *testing.T) {
	ob, err := OutboundOf(&storage.Node{Name: "hy", Address: "a", Port: 443, Protocol: "hysteria2",
		Config: `{"password":"main-pw","obfs":"salamander","obfs_password":"obpw"}`})
	if err != nil {
		t.Fatal(err)
	}
	if ob["password"] != "main-pw" {
		t.Fatalf("password = %v; want main-pw", ob["password"])
	}
	obfs, ok := ob["obfs"].(map[string]any)
	if !ok {
		t.Fatalf("obfs = %#v; want map", ob["obfs"])
	}
	if obfs["type"] != "salamander" || obfs["password"] != "obpw" {
		t.Fatalf("obfs = %#v; want salamander/obpw", obfs)
	}
}
