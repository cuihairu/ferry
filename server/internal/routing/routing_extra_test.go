package routing

import (
	"strings"
	"testing"
)

// TestMergeEdgeBranches 覆盖 IP-only 规则、重复 tag 去重、空 set 跳过、
// 非内置出站不代补、模板 routing 非 map 时重建。
func TestMergeEdgeBranches(t *testing.T) {
	// IP-only 规则（domains 空、ips 非空）。
	out, err := Merge(`{"routing":{"rules":[]},"outbounds":[]}`, []RuleSet{
		{OutboundTag: "direct", IPs: []string{"1.2.3.0/24"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"ip"`) || strings.Contains(out, `"domain"`) {
		t.Fatalf("ip-only rule: %s", out)
	}
	// 空 set（无 domain 无 ip）跳过。
	out, err = Merge(`{}`, []RuleSet{{OutboundTag: "direct"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"rules":[`) && strings.Contains(out, `"direct"`) {
		t.Fatalf("empty set should be skipped: %s", out)
	}
	// 重复 tag 不重复补出站；非内置出站（custom）不代补。
	out, err = Merge(`{"routing":{"rules":[]},"outbounds":[]}`, []RuleSet{
		{OutboundTag: "custom", Domains: []string{"a.com"}},
		{OutboundTag: "custom", Domains: []string{"b.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"tag":"custom"`) {
		t.Fatalf("custom outbound should not be builtin-patched: %s", out)
	}
	// 模板 routing 非 map（如数组）→ 重建。
	out, err = Merge(`{"routing":[1,2],"outbounds":[]}`, []RuleSet{
		{OutboundTag: "direct", Domains: []string{"x.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"rules"`) || !strings.Contains(out, `"direct"`) {
		t.Fatalf("routing non-map should rebuild: %s", out)
	}
	// outbounds 非 slice 类型（如对象）→ 重建。
	out, err = Merge(`{"routing":{"rules":[]},"outbounds":{"a":1}}`, []RuleSet{
		{OutboundTag: "block", Domains: []string{"y.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"tag":"block"`) {
		t.Fatalf("outbounds non-slice should rebuild: %s", out)
	}
}
