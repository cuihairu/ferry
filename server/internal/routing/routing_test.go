package routing

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustMerge(t *testing.T, tmpl string) map[string]any {
	t.Helper()
	out, err := Merge(tmpl, Sets())
	if err != nil {
		t.Fatalf("Merge(%q): %v", tmpl, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("merged output not JSON: %v\n%s", err, out)
	}
	return m
}

func TestMergeEmptyTemplate(t *testing.T) {
	m := mustMerge(t, "")

	rules, _ := m["routing"].(map[string]any)["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("want 2 rules (cn-direct + static-cdn), got %d", len(rules))
	}
	r0 := rules[0].(map[string]any)
	if r0["outboundTag"] != "direct" || r0["type"] != "field" {
		t.Fatalf("rule0 = %v", r0)
	}
	doms, _ := r0["domain"].([]any)
	ips, _ := r0["ip"].([]any)
	if len(doms) != 1 || doms[0] != "geosite:cn" || len(ips) != 1 || ips[0] != "geoip:cn" {
		t.Fatalf("cn-direct 清单缺失: %v", r0)
	}
	r1 := rules[1].(map[string]any)
	if r1["outboundTag"] != "direct" {
		t.Fatalf("rule1 = %v", r1)
	}
	cdnDoms, _ := r1["domain"].([]any)
	if len(cdnDoms) == 0 {
		t.Fatalf("static-cdn 域名清单缺失: %v", r1)
	}
	if _, hasIP := r1["ip"]; hasIP {
		t.Fatalf("static-cdn 是纯域名清单，不应有 ip 段: %v", r1)
	}

	obs, _ := m["outbounds"].([]any)
	if len(obs) != 1 {
		t.Fatalf("want 1 outbound, got %d", len(obs))
	}
	ob := obs[0].(map[string]any)
	if ob["tag"] != "direct" || ob["protocol"] != "freedom" {
		t.Fatalf("direct outbound = %v", ob)
	}
}

func TestMergeInvalidTemplate(t *testing.T) {
	if _, err := Merge("{not-json", Sets()); err == nil {
		t.Fatal("invalid template should error")
	}
}

func TestMergePreservesAndPrepends(t *testing.T) {
	tmpl := `{
		"routing": {"rules": [{"type": "field", "inboundTag": ["api"], "outboundTag": "api"}]},
		"outbounds": [{"tag": "api", "protocol": "freedom"}, {"tag": "relay-out", "protocol": "vless"}]
	}`
	m := mustMerge(t, tmpl)

	rules, _ := m["routing"].(map[string]any)["rules"].([]any)
	if len(rules) != 3 {
		t.Fatalf("want 3 rules (双清单前置+模板既有), got %d", len(rules))
	}
	for i := 0; i < 2; i++ {
		if rules[i].(map[string]any)["outboundTag"] != "direct" {
			t.Fatalf("清单规则应前置，rules[%d] = %v", i, rules[i])
		}
	}
	second := rules[2].(map[string]any)
	if second["outboundTag"] != "api" {
		t.Fatalf("模板既有规则应保留在后，got %v", second)
	}

	obs, _ := m["outbounds"].([]any)
	if len(obs) != 3 {
		t.Fatalf("模板既有出站应保留，want 3, got %d", len(obs))
	}
	if obs[0].(map[string]any)["tag"] != "direct" {
		t.Fatalf("direct 出站缺失时应前置补上，got %v", obs[0])
	}
}

func TestMergeDoesNotDuplicateDirect(t *testing.T) {
	tmpl := `{"outbounds": [{"tag": "direct", "protocol": "freedom", "settings": {"domainStrategy": "UseIP"}}]}`
	m := mustMerge(t, tmpl)

	obs, _ := m["outbounds"].([]any)
	if len(obs) != 1 {
		t.Fatalf("已有 direct 出站不应重复，got %d", len(obs))
	}
	if _, ok := obs[0].(map[string]any)["settings"]; !ok {
		t.Fatalf("既有 direct 出站的字段应原样保留")
	}
}

func TestSetsContainsBuiltinSets(t *testing.T) {
	sets := Sets()
	if len(sets) < 2 {
		t.Fatalf("内置清单应含 cn-direct 与 static-cdn，got %d", len(sets))
	}
	want := map[string]bool{"cn-direct": false, "static-cdn-direct": false}
	for _, s := range sets {
		if _, ok := want[s.Name]; ok && s.OutboundTag == "direct" && len(s.Domains) > 0 {
			want[s.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("内置清单缺失或非法: %s", name)
		}
	}
	// static-cdn 条目必须是 domain: 前缀的字面域名，不依赖规则库数据文件。
	for _, s := range sets {
		if s.Name != "static-cdn-direct" {
			continue
		}
		for _, d := range s.Domains {
			if !strings.HasPrefix(d, "domain:") {
				t.Fatalf("static-cdn 条目应形如 domain:xxx，got %q", d)
			}
		}
	}
}
