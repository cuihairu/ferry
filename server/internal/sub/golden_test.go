package sub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// Golden reference (compatibility gate for the protocol plugin refactor): capture all outputs
// of five protocols x three formats with the pre-refactor implementation and pin them in testdata/golden.json;
// after the refactor compare byte-for-byte and fail when the diff is non-zero.
func TestGoldenSubscriptions(t *testing.T) {
	nodes := []storage.Node{
		{Name: "香港 01", Address: "hk.example.com", Port: 443, Protocol: "vless",
			Config: `{"uuid":"u-vless-1","tls":true,"sni":"sni.example.com","net":"ws","host":"cdn.example.com","path":"/ws","flow":"xtls-rprx-vision"}`},
		{Name: "美西 02", Address: "us.example.com", Port: 8443, Protocol: "vless",
			Config: `{"uuid":"u-vless-2"}`},
		{Name: "东京 03", Address: "jp.example.com", Port: 443, Protocol: "vmess",
			Config: `{"uuid":"u-vmess-1","scy":"aes-128-gcm","aid":64,"tls":true,"sni":"s.jp","net":"ws","path":"/w","host":"h.jp"}`},
		{Name: "法兰 04", Address: "de.example.com", Port: 2053, Protocol: "vmess",
			Config: `{"uuid":"u-vmess-2"}`},
		{Name: "狮城 05", Address: "sg.example.com", Port: 443, Protocol: "trojan",
			Config: `{"password":"pw-trojan","tls":true,"sni":"s.sg","net":"grpc"}`},
		{Name: "伦敦 06", Address: "uk.example.com", Port: 443, Protocol: "trojan",
			Config: `{"password":"pw-uk"}`},
		{Name: "首尔 07", Address: "kr.example.com", Port: 8388, Protocol: "shadowsocks",
			Config: `{"method":"aes-256-gcm","password":"pw-ss"}`},
		{Name: "洛杉矶 08", Address: "la.example.com", Port: 443, Protocol: "hysteria2",
			Config: `{"password":"pw-hy2","sni":"s.la","obfs":"salamander","obfs_password":"op","insecure":true,"up":100,"down":200}`},
		{Name: "悉尼 09", Address: "au.example.com", Port: 8443, Protocol: "hysteria2",
			Config: `{"password":"pw-au"}`},
	}
	type out struct {
		Links       []string            `json:"links"`
		PackV2Ray   string              `json:"pack_v2ray"`
		PackOrdered string              `json:"pack_v2ray_ordered"`
		PackClash   string              `json:"pack_clash"`
		PackSingbox string              `json:"pack_singbox"`
		Outbounds   []map[string]any    `json:"outbounds"`
		SingboxOf   map[string][]string `json:"singbox_of_keys"`
	}
	entries := make([]Entry, len(nodes))
	for i := range nodes {
		entries[i] = Entry{Node: nodes[i]}
	}
	got := out{Links: []string{}, Outbounds: []map[string]any{}, SingboxOf: map[string][]string{}}
	for i := range nodes {
		link, err := ShareLink(&nodes[i])
		if err != nil {
			t.Fatalf("node %d ShareLink: %v", i, err)
		}
		got.Links = append(got.Links, link)
		ob, err := OutboundOf(&nodes[i])
		if err != nil {
			t.Fatalf("node %d OutboundOf: %v", i, err)
		}
		got.Outbounds = append(got.Outbounds, ob)
		keys := make([]string, 0, len(ob))
		for k := range ob {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		got.SingboxOf[nodes[i].Protocol] = keys
	}
	var err error
	if got.PackV2Ray, err = PackV2Ray(entries, []string{"公告一", "公告二"}); err != nil {
		t.Fatalf("PackV2Ray: %v", err)
	}
	if got.PackOrdered, err = PackV2RayOrdered(entries, nil); err != nil {
		t.Fatalf("PackV2RayOrdered: %v", err)
	}
	if got.PackClash, err = PackClash(entries, []string{"note"}); err != nil {
		t.Fatalf("PackClash: %v", err)
	}
	if got.PackSingbox, err = PackSingBox(entries); err != nil {
		t.Fatalf("PackSingBox: %v", err)
	}

	path := filepath.Join("testdata", "golden.json")
	if os.Getenv("GOLDEN_WRITE") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("golden written:", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run GOLDEN_WRITE=1 once): %v", err)
	}
	have, _ := json.MarshalIndent(got, "", "  ")
	if string(have) != string(want) {
		t.Fatalf("golden diff: subscription output changed; regenerate only with review (GOLDEN_WRITE=1)")
	}
}
