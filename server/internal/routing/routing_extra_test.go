package routing

import (
	"strings"
	"testing"
)

// TestMergeEdgeBranches covers IP-only rules, duplicate tag dedup, empty sets skipped,
// non-builtin outbounds not patched, and rebuilding a template whose routing is not a map.
func TestMergeEdgeBranches(t *testing.T) {
	// IP-only rule (domains empty, ips non-empty).
	out, err := Merge(`{"routing":{"rules":[]},"outbounds":[]}`, []RuleSet{
		{OutboundTag: "direct", IPs: []string{"1.2.3.0/24"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"ip"`) || strings.Contains(out, `"domain"`) {
		t.Fatalf("ip-only rule: %s", out)
	}
	// Empty set (no domain, no ip) is skipped.
	out, err = Merge(`{}`, []RuleSet{{OutboundTag: "direct"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"rules":[`) && strings.Contains(out, `"direct"`) {
		t.Fatalf("empty set should be skipped: %s", out)
	}
	// A duplicate tag does not duplicate the outbound patch; non-builtin outbounds (custom) are not patched.
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
	// Template routing not a map (e.g. an array) → rebuild.
	out, err = Merge(`{"routing":[1,2],"outbounds":[]}`, []RuleSet{
		{OutboundTag: "direct", Domains: []string{"x.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"rules"`) || !strings.Contains(out, `"direct"`) {
		t.Fatalf("routing non-map should rebuild: %s", out)
	}
	// outbounds not a slice type (e.g. an object) → rebuild.
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
