// per-sub 过滤维度与配置定制（ferry-sub-filters，2026-11-21 用户拍板）：
// 自建订阅在内存中应用三维度白名单过滤，再按 per-sub 配置重排/截断/改备注。
// 主订阅 /sub/:token 的输出与排序不受影响（PackV2Ray 等原样复用）。
package sub

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Filter 是 per-sub 过滤维度（三维度，全部可选；空数组=该维度不过滤）：
// regions/protocols 是「节点字段 ∈ 数组」白名单，labels 是「节点 labels 与
// 数组有交集」——labels 落库形态是 JSON 数组文本，解析失败视为无标签。
type Filter struct {
	Regions   []string `json:"regions,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Protocols []string `json:"protocols,omitempty"`
}

// Config 是 per-sub 下发配置定制（全部可选，缺省沿用现有行为）。
// IncludeRTT 用指针区分「显式 false」与「缺席」（缺席=沿用现状=带延迟）。
type Config struct {
	RemarkTemplate string `json:"remark_template,omitempty"` // 占位符 {region} {name} {rtt}
	Sort           string `json:"sort,omitempty"`            // region|rtt|name
	MaxNodes       int    `json:"max_nodes,omitempty"`       // 下发节点数上限，0=不截断
	IncludeRTT     *bool  `json:"include_rtt,omitempty"`     // false=备注不带 RTT 段
}

// decodeStrictJSON 严格解码 JSON 文本：未知字段报错（严格模式，防拼写错误
// 静默失效成「看似配置了实则不过滤」）。
func decodeStrictJSON(s string, v any) error {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

// ParseFilter 严格解析 filter JSON 文本；空文本返回零值 Filter（不过滤）。
func ParseFilter(s string) (Filter, error) {
	var f Filter
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return f, nil
	}
	if err := decodeStrictJSON(trimmed, &f); err != nil {
		return Filter{}, err
	}
	return f, nil
}

// ParseConfig 严格解析 config JSON 文本；空文本返回零值 Config（沿用现状）。
func ParseConfig(s string) (Config, error) {
	var c Config
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return c, nil
	}
	if err := decodeStrictJSON(trimmed, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// ApplyFilter 在内存中按 Filter 过滤入口：region/protocol 是节点字段白名单，
// labels 是交集判定。三维度全空=原样返回。
func ApplyFilter(entries []Entry, f Filter) []Entry {
	if len(f.Regions) == 0 && len(f.Labels) == 0 && len(f.Protocols) == 0 {
		return entries
	}
	out := make([]Entry, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		if len(f.Regions) > 0 && !containsString(f.Regions, e.Node.Region) {
			continue
		}
		if len(f.Protocols) > 0 && !containsString(f.Protocols, e.Node.Protocol) {
			continue
		}
		if len(f.Labels) > 0 {
			// 节点 labels 是 JSON 数组文本；解析失败视为无标签（不 500）。
			if !intersects(f.Labels, nodeLabels(e.Node.Labels)) {
				continue
			}
		}
		out = append(out, *e)
	}
	return out
}

// SortEntries 按 sort 模式重排入口副本：region=按区域分组序（同主订阅
// orderByProbe：区域归组、组内 RTT 升序、同档轮转），rtt=全局延迟升序
// （无数据殿后），name=名称序；缺省/空串沿用现状（region）。
func SortEntries(entries []Entry, mode string, now time.Time) []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	switch mode {
	case "rtt":
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i].RttMs, out[j].RttMs
			// 有结论（>0）在前、无结论（0）殿后；同档按 RTT 升序。
			if (a > 0) != (b > 0) {
				return a > 0
			}
			return a < b
		})
	case "name":
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].Node.Name < out[j].Node.Name
		})
	default: // "" 与 "region" 都走区域分组序（现状口径）
		return orderByProbe(out, now)
	}
	return out
}

// ApplyConfig 对已过滤的入口应用 per-sub 配置：先按 sort 重排，max_nodes>0
// 时截断，最后逐条按 remark_template/include_rtt 生成备注写入 Entry.Remark。
func ApplyConfig(entries []Entry, cfg Config, now time.Time) []Entry {
	out := SortEntries(entries, cfg.Sort, now)
	if cfg.MaxNodes > 0 && len(out) > cfg.MaxNodes {
		out = out[:cfg.MaxNodes]
	}
	for i := range out {
		out[i].Remark = BuildRemark(&out[i], cfg)
	}
	return out
}

// BuildRemark 按 config 生成单条备注：remark_template 非空时按占位符替换
// （{region}/{name}/{rtt}，非法模板字符原样输出不 panic），空则回退默认
// 「区域 · 名称 (延迟ms)」；include_rtt=false 或无测速数据时省略 RTT。
func BuildRemark(e *Entry, cfg Config) string {
	tpl := cfg.RemarkTemplate
	includeRTT := cfg.IncludeRTT == nil || *cfg.IncludeRTT
	if tpl == "" {
		if includeRTT {
			return entryRemark(e)
		}
		return fmt.Sprintf("%s · %s", entryRegion(e), e.Node.Name)
	}
	rtt := ""
	if includeRTT && e.RttMs > 0 {
		rtt = strconv.Itoa(e.RttMs)
	}
	return strings.NewReplacer("{region}", entryRegion(e), "{name}", e.Node.Name, "{rtt}", rtt).Replace(tpl)
}

// nodeLabels 解析节点 labels 的 JSON 数组文本；空文本、非数组与解析失败
// 一律视为无标签（下发侧容错，不 500）。
func nodeLabels(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// intersects 判定两个字符串集合是否有交集。
func intersects(a, b []string) bool {
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	for _, y := range b {
		if set[y] {
			return true
		}
	}
	return false
}
