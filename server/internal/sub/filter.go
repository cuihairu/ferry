// per-sub filter dimensions and config customization (ferry-sub-filters, user-ratified 2026-11-21):
// Self-built subscriptions apply three-dimension whitelist filtering in memory, then reorder/truncate/re-remark per per-sub config.
// The output and ordering of the main subscription /sub/:token are unaffected (PackV2Ray etc. reused as-is).
package sub

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Filter is the per-sub filter dimensions (three dimensions, all optional; empty array = no filtering for that dimension):
// regions/protocols are whitelists of "node field in array"; labels are "node labels with
// non-empty intersection"; labels are stored as JSON array text; parse failure is treated as no labels.
type Filter struct {
	Regions   []string `json:"regions,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Protocols []string `json:"protocols,omitempty"`
}

// Config is per-sub delivery config customization (all optional; defaults keep existing behavior).
// IncludeRTT uses a pointer to distinguish "explicitly false" from "absent" (absent = keep current = include RTT).
type Config struct {
	RemarkTemplate string `json:"remark_template,omitempty"` // placeholders {region} {name} {rtt}
	Sort           string `json:"sort,omitempty"`            // region|rtt|name
	MaxNodes       int    `json:"max_nodes,omitempty"`       // max number of nodes to deliver; 0=do not truncate
	IncludeRTT     *bool  `json:"include_rtt,omitempty"`     // false=remark omits the RTT segment
}

// decodeStrictJSON strictly decodes JSON text: unknown fields error (strict mode, to catch typos
// from silently degrading to "looks configured but actually not filtering").
func decodeStrictJSON(s string, v any) error {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

// ParseFilter strictly parses the filter JSON text; empty text returns a zero-value Filter (no filtering).
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

// ParseConfig strictly parses the config JSON text; empty text returns a zero-value Config (keep current behavior).
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

// ApplyFilter filters entries in memory by Filter: region/protocol are node-field whitelists,
// labels are intersection checks. All three empty = return as-is.
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
			// node labels are JSON array text; parse failure is treated as no labels (no 500).
			if !intersects(f.Labels, nodeLabels(e.Node.Labels)) {
				continue
			}
		}
		out = append(out, *e)
	}
	return out
}

// SortEntries reorders an entry copy by the sort mode: region=region-group order (same as main subscription
// orderByProbe: region grouping, ascending RTT within group, rotation in same tier), rtt=global ascending latency
// (no data last), name=name order; default/empty string keeps current behavior (region).
func SortEntries(entries []Entry, mode string, now time.Time) []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	switch mode {
	case "rtt":
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i].RttMs, out[j].RttMs
			// Verdict known (>0) first, unknown (0) last; same tier by ascending RTT.
			if (a > 0) != (b > 0) {
				return a > 0
			}
			return a < b
		})
	case "name":
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].Node.Name < out[j].Node.Name
		})
	default: // "" and "region" both use region-group order (current semantics)
		return orderByProbe(out, now)
	}
	return out
}

// ApplyConfig applies per-sub config to filtered entries: reorder by sort first, and when max_nodes>0
// truncate; finally generate the remark per entry from remark_template/include_rtt into Entry.Remark.
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

// BuildRemark generates a single remark by config: when remark_template is non-empty, substitute placeholders
// ({region}/{name}/{rtt}/{labels}; invalid template characters are emitted as-is, no panic); when empty, fall back to
// the default "region · name (latencyms)"; omit RTT when include_rtt=false or when there is no probe data. {labels}
// joins the node's label list with commas (F-1107: labels join region and the sort modes as a unified output
// dimension); a node with no labels substitutes the empty string.
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
	labels := strings.Join(nodeLabels(e.Node.Labels), ",")
	return strings.NewReplacer("{region}", entryRegion(e), "{name}", e.Node.Name, "{rtt}", rtt,
		"{labels}", labels).Replace(tpl)
}

// nodeLabels parses the JSON array text of node labels; empty text, non-array, and parse failure
// are all treated as no labels (tolerant on the delivery side, no 500).
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

// intersects reports whether two string sets have a non-empty intersection.
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
