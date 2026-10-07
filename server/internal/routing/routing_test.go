package routing

import (
	"encoding/json"
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
	if len(rules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(rules))
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
	if len(rules) != 2 {
		t.Fatalf("want 2 rules (清单前置+模板既有), got %d", len(rules))
	}
	first := rules[0].(map[string]any)
	if first["outboundTag"] != "direct" {
		t.Fatalf("清单规则应前置，got %v", first)
	}
	second := rules[1].(map[string]any)
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

func TestSetsContainsCNDirect(t *testing.T) {
	sets := Sets()
	if len(sets) == 0 {
		t.Fatal("Sets() 不应为空")
	}
	found := false
	for _, s := range sets {
		if s.Name == "cn-direct" && s.OutboundTag == "direct" {
			found = true
		}
	}
	if !found {
		t.Fatal("内置清单应含 cn-direct")
	}
}
