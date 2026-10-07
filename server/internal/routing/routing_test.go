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
	if len(rules) != 3 {
		t.Fatalf("want 3 rules (cn-direct + static-cdn + ad-block), got %d", len(rules))
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
	// 广告拦截（SAVE-4）：命中发 block 丢弃
	r2 := rules[2].(map[string]any)
	if r2["outboundTag"] != "block" {
		t.Fatalf("rule2 = %v", r2)
	}
	adsDoms, _ := r2["domain"].([]any)
	if len(adsDoms) != 1 || adsDoms[0] != "geosite:category-ads-all" {
		t.Fatalf("ad-block 清单缺失: %v", r2)
	}

	obs, _ := m["outbounds"].([]any)
	if len(obs) != 2 {
		t.Fatalf("want 2 outbounds (block + direct), got %d", len(obs))
	}
	ob := obs[0].(map[string]any)
	if ob["tag"] != "block" || ob["protocol"] != "blackhole" {
		t.Fatalf("block outbound = %v", ob)
	}
	ob = obs[1].(map[string]any)
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
	if len(rules) != 4 {
		t.Fatalf("want 4 rules (三清单前置+模板既有), got %d", len(rules))
	}
	for i := 0; i < 3; i++ {
		tag, _ := rules[i].(map[string]any)["outboundTag"].(string)
		if tag != "direct" && tag != "block" {
			t.Fatalf("清单规则应前置，rules[%d] = %v", i, rules[i])
		}
	}
	second := rules[3].(map[string]any)
	if second["outboundTag"] != "api" {
		t.Fatalf("模板既有规则应保留在后，got %v", second)
	}

	obs, _ := m["outbounds"].([]any)
	if len(obs) != 4 {
		t.Fatalf("模板既有出站应保留，want 4, got %d", len(obs))
	}
	if obs[0].(map[string]any)["tag"] != "block" {
		t.Fatalf("block 出站缺失时应前置补上，got %v", obs[0])
	}
	if obs[1].(map[string]any)["tag"] != "direct" {
		t.Fatalf("direct 出站缺失时应前置补上，got %v", obs[1])
	}
	if obs[2].(map[string]any)["tag"] != "api" || obs[3].(map[string]any)["tag"] != "relay-out" {
		t.Fatalf("模板既有出站顺序应不变: %v %v", obs[2], obs[3])
	}
}

func TestMergeDoesNotDuplicateDirect(t *testing.T) {
	tmpl := `{"outbounds": [{"tag": "direct", "protocol": "freedom", "settings": {"domainStrategy": "UseIP"}}]}`
	m := mustMerge(t, tmpl)

	obs, _ := m["outbounds"].([]any)
	if len(obs) != 2 {
		t.Fatalf("已有 direct 出站不应重复，want 2 (direct 保留 + block 补上), got %d", len(obs))
	}
	var direct map[string]any
	for _, ob := range obs {
		if m, ok := ob.(map[string]any); ok && m["tag"] == "direct" {
			direct = m
		}
	}
	if direct == nil {
		t.Fatalf("direct 出站丢失: %v", obs)
	}
	if _, ok := direct["settings"]; !ok {
		t.Fatalf("既有 direct 出站的字段应原样保留")
	}
}

// TestMergeDoesNotDuplicateBlock 覆盖模板自带 block 出站（SAVE-4）：
// 不重复补，自定义字段原样保留。
func TestMergeDoesNotDuplicateBlock(t *testing.T) {
	tmpl := `{"outbounds": [{"tag": "block", "protocol": "blackhole", "settings": {"response": {"type": "none"}}}]}`
	m := mustMerge(t, tmpl)

	obs, _ := m["outbounds"].([]any)
	if len(obs) != 2 {
		t.Fatalf("已有 block 出站不应重复，want 2 (block 保留 + direct 补上), got %d", len(obs))
	}
	var block map[string]any
	for _, ob := range obs {
		if m, ok := ob.(map[string]any); ok && m["tag"] == "block" {
			block = m
		}
	}
	if block == nil {
		t.Fatalf("block 出站丢失: %v", obs)
	}
	if _, ok := block["settings"]; !ok {
		t.Fatalf("既有 block 出站的字段应原样保留")
	}
}

// TestMergeStableRender 覆盖渲染确定性（SAVE-4）：同模板多次 Merge
// 输出一致（sha256 稳定，出站补齐不依赖 map 迭代序）。
func TestMergeStableRender(t *testing.T) {
	first, err := Merge(`{}`, Sets())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := Merge(`{}`, Sets())
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if again != first {
			t.Fatalf("render not stable:\n%s\n%s", first, again)
		}
	}
}

func TestSetsContainsBuiltinSets(t *testing.T) {
	sets := Sets()
	if len(sets) < 3 {
		t.Fatalf("内置清单应含 cn-direct/static-cdn/ad-block，got %d", len(sets))
	}
	want := map[string]bool{"cn-direct": false, "static-cdn-direct": false, "ad-block": false}
	for _, s := range sets {
		if _, ok := want[s.Name]; ok && len(s.Domains) > 0 {
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

// TestWithoutAds 覆盖广告拦截开关的清单过滤（SAVE-4）：只滤 ad-block，
// 其余清单原序保留。
func TestWithoutAds(t *testing.T) {
	filtered := WithoutAds(Sets())
	if len(filtered) != len(Sets())-1 {
		t.Fatalf("want %d sets after filter, got %d", len(Sets())-1, len(filtered))
	}
	for _, s := range filtered {
		if s.Name == "ad-block" {
			t.Fatal("ad-block must be filtered out")
		}
	}
	if filtered[0].Name != "cn-direct" || filtered[1].Name != "static-cdn-direct" {
		t.Fatalf("其余清单应原序保留: %v", filtered)
	}
	// 过滤后渲染不再产生 block 规则与出站
	m := mustMergeWith(t, "", filtered)
	rules, _ := m["routing"].(map[string]any)["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("want 2 rules without ads, got %d", len(rules))
	}
	obs, _ := m["outbounds"].([]any)
	for _, ob := range obs {
		if m, ok := ob.(map[string]any); ok && m["tag"] == "block" {
			t.Fatalf("block outbound must be absent without ads: %v", obs)
		}
	}
}

func mustMergeWith(t *testing.T, tmpl string, sets []RuleSet) map[string]any {
	t.Helper()
	out, err := Merge(tmpl, sets)
	if err != nil {
		t.Fatalf("Merge(%q): %v", tmpl, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("merged output not JSON: %v\n%s", err, out)
	}
	return m
}
