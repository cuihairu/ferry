package sub

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gopkg.in/yaml.v3"
)

// shapeEntries is a two-node fleet in one region: enough for a real region
// group on the clash family.
func shapeEntries() []Entry {
	node := func(name, addr string) Entry {
		return Entry{Node: storage.Node{Name: name, Address: addr, Port: 443,
			Protocol: "vless", Config: `{"uuid":"u"}`, Region: "香港"}}
	}
	return []Entry{node("hk-1", "a.example.com"), node("hk-2", "b.example.com")}
}

// clashShape is the proxy-group skeleton the group-shape test reads back.
type clashShape struct {
	Groups []struct {
		Name    string   `yaml:"name"`
		Type    string   `yaml:"type"`
		Lazy    *bool    `yaml:"lazy"`
		Proxies []string `yaml:"proxies"`
	} `yaml:"proxy-groups"`
}

// TestProxyGroupShapesPerTarget pins the per-client group vocabulary (F-1132):
// no target assumes another client's proxy-group capability -- the clash
// family groups with the core url-test shape (lazy off on plain clash, on
// only for mihomo per the capability matrix), while the v2ray family and the
// JSON targets carry no groups at all (their clients build their own).
func TestProxyGroupShapesPerTarget(t *testing.T) {
	// plain clash: url-test groups, never a lazy key (not core vocabulary)
	out, err := PackClash(shapeEntries(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc clashShape
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Groups) != 2 || doc.Groups[0].Name != "PROXY" || doc.Groups[0].Type != "select" {
		t.Fatalf("clash groups = %+v", doc.Groups)
	}
	region := doc.Groups[1]
	if region.Type != "url-test" || len(region.Proxies) != 2 {
		t.Fatalf("clash region group = %+v", region)
	}
	if region.Lazy != nil {
		t.Fatalf("plain clash must not carry a lazy key: %+v", region)
	}

	// mihomo: same core shape plus the meta-only lazy extension
	out, err = PackMihomo(shapeEntries(), nil)
	if err != nil {
		t.Fatal(err)
	}
	doc = clashShape{}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Groups) != 2 || doc.Groups[1].Type != "url-test" {
		t.Fatalf("mihomo groups = %+v", doc.Groups)
	}
	if doc.Groups[1].Lazy == nil || !*doc.Groups[1].Lazy {
		t.Fatalf("mihomo region group must be lazy: %+v", doc.Groups[1])
	}

	// v2ray family: grouping lives in sort + remark only -- decoded lines are
	// links and comments, nothing else (no group syntax exists to assume).
	out, err = PackV2Ray(shapeEntries(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if line != "" && !strings.HasPrefix(line, "vless://") && !strings.HasPrefix(line, "# ") {
			t.Fatalf("v2ray line is neither link nor comment: %q", line)
		}
	}

	// JSON targets: pure outbounds arrays, no group object anywhere.
	for target, pack := range map[string]func([]Entry, []string) (string, error){
		"xray":    func(e []Entry, _ []string) (string, error) { return PackXray(e) },
		"singbox": func(e []Entry, _ []string) (string, error) { return PackSingBox(e) },
	} {
		out, err := pack(shapeEntries(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "proxy-group") || strings.Contains(out, "url-test") {
			t.Fatalf("%s output must not carry groups:\n%s", target, out)
		}
	}
}
