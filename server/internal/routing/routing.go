// Package routing generates the split-routing rule section of node config templates (traffic saving design §1/§2):
// traffic matching a rule set connects directly, skipping the tunnel and saving landing traffic; the rest goes to the
// template's existing outbounds. Rule-set data files (geoip.dat/geosite.dat) distributed via config.push are resolved by the
// core; this package only renders the rule-set references into the template and guarantees the direct outbound exists.
package routing

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RuleSet is a group of split-routing rules with one destination: matching any domain or IP rule sends traffic to OutboundTag.
// Domains/IPs use the core rule syntax (geosite:cn, geoip:cn, domain:, CIDR, etc.) and pass through unchanged.
type RuleSet struct {
	Name        string   `json:"name"` // rule-set name, kept in the config snapshot note
	Domains     []string `json:"domains,omitempty"`
	IPs         []string `json:"ips,omitempty"`
	OutboundTag string   `json:"outbound_tag"` // outbound tag used on a match
}

// DirectCN is the domestic domain/IP direct rule set (SAVE-1): geosite:cn + geoip:cn, with the data files
// (geosite.dat/geoip.dat) distributed to the node asset directory via config.push.
var DirectCN = RuleSet{
	Name:        "cn-direct",
	Domains:     []string{"geosite:cn"},
	IPs:         []string{"geoip:cn"},
	OutboundTag: "direct",
}

// StaticCDN is the direct rule set for public static-resource/CDN domains (SAVE-2): those resources are reachable and
// free domestically, so tunneling them would needlessly waste landing traffic. Entries use the domain: prefix with
// literal domains (including subdomain matching) and do not depend on rule-set data files.
var StaticCDN = RuleSet{
	Name: "static-cdn-direct",
	Domains: []string{
		"domain:bootcdn.cn",
		"domain:bootcdn.net",
		"domain:staticfile.org",
		"domain:staticfile.net",
		"domain:bootcss.com",
		"domain:jsdelivr.net",
		"domain:unpkg.com",
		"domain:npmmirror.com",
	},
	OutboundTag: "direct",
}

// AdBlock is the ad/tracking domain block rule set (SAVE-4): a match is sent to the block outbound
// (blackhole drop), saving landing traffic and avoiding extra connections. The list data files are distributed and
// updated via rulelib with the same mechanism as geoip/geosite (open-source lists such as anti-AD can push a geosite file in the same format).
var AdBlock = RuleSet{
	Name:        "ad-block",
	Domains:     []string{"geosite:category-ads-all"},
	OutboundTag: "block",
}

// Sets returns the builtin split-routing rule sets, rendered into the template in declaration order.
func Sets() []RuleSet {
	return []RuleSet{DirectCN, StaticCDN, AdBlock}
}

// WithoutAds filters out the ad-blocking rule set (the rendering model when the SAVE-4 switch is off).
func WithoutAds(sets []RuleSet) []RuleSet {
	out := make([]RuleSet, 0, len(sets))
	for _, s := range sets {
		if s.Name == AdBlock.Name {
			continue
		}
		out = append(out, s)
	}
	return out
}

// builtinOutbounds are the builtin outbound forms where split-routing rules land: referenced outbounds
// are added per this table when missing (direct = freedom direct connect, block = blackhole drop).
var builtinOutbounds = map[string]map[string]any{
	"direct": {"tag": "direct", "protocol": "freedom"},
	"block":  {"tag": "block", "protocol": "blackhole"},
}

// Merge renders the rule sets into routing rules and injects them into the template JSON:
// rules are prepended (matched before the template's existing rules, avoiding being intercepted by a catch-all rule),
// outbounds missing direct get it prepended. An empty template object produces a minimal skeleton.
func Merge(templateJSON string, sets []RuleSet) (string, error) {
	tmpl := map[string]any{}
	if s := strings.TrimSpace(templateJSON); s != "" {
		if err := json.Unmarshal([]byte(s), &tmpl); err != nil {
			return "", fmt.Errorf("template is not valid JSON: %w", err)
		}
		if tmpl == nil {
			tmpl = map[string]any{}
		}
	}

	rules := make([]any, 0, len(sets))
	for _, set := range sets {
		if len(set.Domains) == 0 && len(set.IPs) == 0 {
			continue
		}
		rule := map[string]any{"type": "field", "outboundTag": set.OutboundTag}
		if len(set.Domains) > 0 {
			rule["domain"] = set.Domains
		}
		if len(set.IPs) > 0 {
			rule["ip"] = set.IPs
		}
		rules = append(rules, rule)
	}

	// routing.rules: prepend the rule-set rules, keeping the template's existing rules.
	routingMap, _ := tmpl["routing"].(map[string]any)
	if routingMap == nil {
		routingMap = map[string]any{}
	}
	existingRules, _ := routingMap["rules"].([]any)
	merged := append(append([]any{}, rules...), existingRules...)
	routingMap["rules"] = merged
	tmpl["routing"] = routingMap

	// outbounds: referenced outbounds are prepended in builtin form when missing
	// (direct = freedom, block = blackhole). Filling them in rule-set declaration order keeps the render stable.
	outbounds, _ := tmpl["outbounds"].([]any)
	ensured := map[string]bool{}
	for _, set := range sets {
		if len(set.Domains) == 0 && len(set.IPs) == 0 {
			continue
		}
		tag := set.OutboundTag
		if ensured[tag] {
			continue
		}
		ensured[tag] = true
		builtin, ok := builtinOutbounds[tag]
		if !ok {
			continue // non-builtin destinations (the template's own outbounds) are not patched
		}
		found := false
		for _, ob := range outbounds {
			if m, ok := ob.(map[string]any); ok && m["tag"] == tag {
				found = true
				break
			}
		}
		if !found {
			cp := map[string]any{}
			for k, v := range builtin {
				cp[k] = v
			}
			outbounds = append([]any{cp}, outbounds...)
		}
	}
	tmpl["outbounds"] = outbounds

	out, err := json.Marshal(tmpl)
	if err != nil {
		return "", fmt.Errorf("marshal merged template: %w", err)
	}
	return string(out), nil
}
