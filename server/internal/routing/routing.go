// Package routing 生成节点配置模板的分流规则段（流量节省设计 §1/§2）：
// 命中清单的流量直连不进隧道、不耗落地流量，其余走模板既有出站。
// 清单经 config.push 分发的规则库数据文件（geoip.dat/geosite.dat）由内核解析，
// 本包只负责把清单引用渲染进模板并保证 direct 出站存在。
package routing

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RuleSet 是一组同去向的分流规则：域名与 IP 清单命中任一即发往 OutboundTag。
// Domains/IPs 为内核规则语法（geosite:cn、geoip:cn、domain:、CIDR 等），原样透传。
type RuleSet struct {
	Name        string   `json:"name"` // 清单名，入配置快照备注
	Domains     []string `json:"domains,omitempty"`
	IPs         []string `json:"ips,omitempty"`
	OutboundTag string   `json:"outbound_tag"` // 命中后的出站 tag
}

// DirectCN 是国内域名/IP 直连清单（SAVE-1）：geosite:cn + geoip:cn，
// 数据文件（geosite.dat/geoip.dat）经 config.push 分发到节点资产目录。
var DirectCN = RuleSet{
	Name:        "cn-direct",
	Domains:     []string{"geosite:cn"},
	IPs:         []string{"geoip:cn"},
	OutboundTag: "direct",
}

// StaticCDN 是公共静态资源/CDN 域名直连清单（SAVE-2）：这些域名的资源
// 国内可达且免费，走隧道纯属浪费落地流量。条目用 domain: 前缀字面域名
// （含子域匹配），不依赖规则库数据文件。
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

// AdBlock 是广告/追踪域名屏蔽清单（SAVE-4）：命中即发 block 出站
// （blackhole 丢弃），省落地流量也少建连。名单数据文件与 geoip/geosite
// 同机制经 rulelib 分发更新（anti-AD 等开源名单可推同格式 geosite 文件）。
var AdBlock = RuleSet{
	Name:        "ad-block",
	Domains:     []string{"geosite:category-ads-all"},
	OutboundTag: "block",
}

// Sets 返回内置分流清单，按声明序渲染进模板。
func Sets() []RuleSet {
	return []RuleSet{DirectCN, StaticCDN, AdBlock}
}

// WithoutAds 滤掉广告拦截清单（SAVE-4 开关关时的渲染口径）。
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

// builtinOutbounds 是分流规则落点的内置出站形态：清单引用的出站
// 缺失时按此补上（direct=freedom 直连，block=blackhole 丢弃）。
var builtinOutbounds = map[string]map[string]any{
	"direct": {"tag": "direct", "protocol": "freedom"},
	"block":  {"tag": "block", "protocol": "blackhole"},
}

// Merge 把清单渲染成 routing 规则并注入模板 JSON：
// 规则前置（先于模板既有规则匹配，避免被兜底规则截胡），
// outbounds 缺 direct 时前置补上。模板为空对象时生成最小骨架。
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

	// routing.rules：清单规则前置，保留模板既有规则。
	routingMap, _ := tmpl["routing"].(map[string]any)
	if routingMap == nil {
		routingMap = map[string]any{}
	}
	existingRules, _ := routingMap["rules"].([]any)
	merged := append(append([]any{}, rules...), existingRules...)
	routingMap["rules"] = merged
	tmpl["routing"] = routingMap

	// outbounds：清单规则引用的出站缺失时按内置形态前置补上
	// （direct=freedom，block=blackhole）。按清单声明序补齐保证渲染稳定。
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
			continue // 非内置落点（模板自有出站）不代补
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
