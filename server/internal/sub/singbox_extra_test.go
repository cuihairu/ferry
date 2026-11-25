package sub

import (
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSingBoxHysteria2ObfsPassword covers full expansion of the hysteria2 obfs password: the obfs object
// carries type and password, while the main password stays at the top level.
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
