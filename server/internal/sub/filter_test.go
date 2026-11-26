// per-sub filter and config customization pure-function unit tests (ferry-sub-filters):
// ApplyFilter: each dimension hit/miss/empty-array-no-filter, labels bad-text tolerance;
// SortEntries/BuildRemark/ApplyConfig config items take effect; ParseFilter/ParseConfig
// strict mode (the unknown-field 400 verdict lives at this layer).
package sub

import (
	"reflect"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// nodeOf builds a test node.
func nodeOf(name, region, labels, protocol string) storage.Node {
	return storage.Node{Name: name, Region: region, Labels: labels, Protocol: protocol}
}

func ent(name, region, labels, protocol string, rtt int) Entry {
	return Entry{Node: nodeOf(name, region, labels, protocol), RttMs: rtt}
}

func TestApplyFilter(t *testing.T) {
	entries := []Entry{
		ent("hk1", "香港", `["streaming","cn"]`, "vless", 20),
		ent("hy", "香港", `["streaming"]`, "hysteria2", 30),
		ent("us", "美国", `["game"]`, "vmess", 40),
		ent("jp", "日本", "", "trojan", 50),
	}
	t.Run("region whitelist hit/miss", func(t *testing.T) {
		got := ApplyFilter(entries, Filter{Regions: []string{"香港"}})
		if len(got) != 2 || got[0].Node.Name != "hk1" || got[1].Node.Name != "hy" {
			t.Fatalf("region filter = %+v", names(got))
		}
		if n := ApplyFilter(entries, Filter{Regions: []string{"不存在"}}); len(n) != 0 {
			t.Fatalf("region miss leaked: %v", names(n))
		}
	})
	t.Run("protocol whitelist", func(t *testing.T) {
		got := ApplyFilter(entries, Filter{Protocols: []string{"hysteria2"}})
		if len(got) != 1 || got[0].Node.Name != "hy" {
			t.Fatalf("protocol filter = %+v", names(got))
		}
	})
	t.Run("labels intersect hit/miss", func(t *testing.T) {
		got := ApplyFilter(entries, Filter{Labels: []string{"streaming"}})
		if len(got) != 2 || got[0].Node.Name != "hk1" || got[1].Node.Name != "hy" {
			t.Fatalf("labels filter = %+v", names(got))
		}
		if n := ApplyFilter(entries, Filter{Labels: []string{"unblock"}}); len(n) != 0 {
			t.Fatalf("labels miss leaked: %v", names(n))
		}
	})
	t.Run("empty arrays no filtering", func(t *testing.T) {
		got := ApplyFilter(entries, Filter{Regions: []string{}, Labels: []string{}, Protocols: []string{}})
		if len(got) != len(entries) {
			t.Fatalf("empty filter filtered: %v", names(got))
		}
	})
	t.Run("combined all dimensions", func(t *testing.T) {
		got := ApplyFilter(entries, Filter{
			Regions:   []string{"香港"},
			Protocols: []string{"hysteria2"},
			Labels:    []string{"streaming"},
		})
		if len(got) != 1 || got[0].Node.Name != "hy" {
			t.Fatalf("combined filter = %+v", names(got))
		}
	})
	t.Run("labels bad text tolerated", func(t *testing.T) {
		bad := []Entry{
			ent("x1", "香港", `{not-json`, "vless", 10),
			ent("x2", "香港", `{"a":1}`, "vmess", 10), // not an array
		}
		// Bad text is treated as no labels: label filter misses (no panic, no 500).
		if n := ApplyFilter(bad, Filter{Labels: []string{"anything"}}); len(n) != 0 {
			t.Fatalf("bad labels leaked: %v", names(n))
		}
		// With no filtering dimensions, bad-text nodes still come through.
		if n := ApplyFilter(bad, Filter{}); len(n) != 2 {
			t.Fatalf("bad labels no-filter count = %d", len(n))
		}
	})
}

func TestSortEntries(t *testing.T) {
	entries := []Entry{
		ent("b", "美国", "", "vless", 50),
		ent("a", "香港", "", "vless", 20),
		ent("c", "香港", "", "vless", 0),
		ent("d", "香港", "", "vless", 30),
	}
	now := time.Unix(0, 0) // fixed time, rotation slot deterministic
	t.Run("region default", func(t *testing.T) {
		got := SortEntries(entries, "", now)
		// Regions grouped by string byte order (US < HK), ascending rtt within group, unknown last;
		// at now=0 the rotation offset is 0, no rotation.
		want := []string{"b", "a", "d", "c"}
		if !reflect.DeepEqual(names(got), want) {
			t.Fatalf("region default = %v, want %v", names(got), want)
		}
	})
	t.Run("rtt ascending unknown last", func(t *testing.T) {
		got := SortEntries(entries, "rtt", now)
		want := []string{"a", "d", "b", "c"} // 20,30,50,0(unknown last)
		if !reflect.DeepEqual(names(got), want) {
			t.Fatalf("rtt sort = %v, want %v", names(got), want)
		}
	})
	t.Run("name ascending", func(t *testing.T) {
		got := SortEntries(entries, "name", now)
		want := []string{"a", "b", "c", "d"}
		if !reflect.DeepEqual(names(got), want) {
			t.Fatalf("name sort = %v, want %v", names(got), want)
		}
	})
}

func TestBuildRemark(t *testing.T) {
	e := ent("hk1", "香港", "", "vless", 42)
	t.Run("template full replace", func(t *testing.T) {
		got := BuildRemark(&e, Config{RemarkTemplate: "{region} · {name} ({rtt}ms)"})
		if got != "香港 · hk1 (42ms)" {
			t.Fatalf("template = %q", got)
		}
	})
	t.Run("template rtt unknown empty", func(t *testing.T) {
		z := ent("z", "未知", "", "vless", 0)
		got := BuildRemark(&z, Config{RemarkTemplate: "{region}-{name}-{rtt}"})
		if got != "未知-z-" {
			t.Fatalf("unknown rtt template = %q", got)
		}
	})
	t.Run("include_rtt false clears rtt", func(t *testing.T) {
		f := false
		got := BuildRemark(&e, Config{RemarkTemplate: "{region} {rtt}", IncludeRTT: &f})
		if got != "香港 " {
			t.Fatalf("include_rtt false = %q", got)
		}
	})
	t.Run("default remark with rtt", func(t *testing.T) {
		if got := BuildRemark(&e, Config{}); got != "香港 · hk1 (42ms)" {
			t.Fatalf("default = %q", got)
		}
	})
	t.Run("default remark no rtt", func(t *testing.T) {
		f := false
		if got := BuildRemark(&e, Config{IncludeRTT: &f}); got != "香港 · hk1" {
			t.Fatalf("default no rtt = %q", got)
		}
	})
	t.Run("unknown placeholder literal no panic", func(t *testing.T) {
		got := BuildRemark(&e, Config{RemarkTemplate: "{region}{nope}{name}"})
		if got != "香港{nope}hk1" {
			t.Fatalf("literal = %q", got)
		}
	})
}

func TestApplyConfig(t *testing.T) {
	entries := []Entry{
		ent("c", "美国", "", "vless", 30),
		ent("a", "香港", "", "vless", 10),
		ent("b", "香港", "", "vless", 20),
	}
	t.Run("max_nodes truncates", func(t *testing.T) {
		got := ApplyConfig(entries, Config{Sort: "name", MaxNodes: 2}, time.Unix(0, 0))
		if len(got) != 2 || got[0].Node.Name != "a" || got[1].Node.Name != "b" {
			t.Fatalf("truncate = %+v", names(got))
		}
	})
	t.Run("sort and remark applied", func(t *testing.T) {
		got := ApplyConfig(entries, Config{
			Sort:           "rtt",
			RemarkTemplate: "{region}/{name}/{rtt}",
		}, time.Unix(0, 0))
		if len(got) != 3 || got[0].Node.Name != "a" {
			t.Fatalf("sort = %+v", names(got))
		}
		if got[0].Remark != "香港/a/10" {
			t.Fatalf("remark = %q", got[0].Remark)
		}
	})
}

func TestParseFilter(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		f, err := ParseFilter(`{"regions":["香港"],"labels":["streaming"],"protocols":["hysteria2"]}`)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.Regions, []string{"香港"}) || !reflect.DeepEqual(f.Labels, []string{"streaming"}) ||
			!reflect.DeepEqual(f.Protocols, []string{"hysteria2"}) {
			t.Fatalf("parsed = %+v", f)
		}
	})
	t.Run("unknown field rejected", func(t *testing.T) {
		if _, err := ParseFilter(`{"regoin":["香港"]}`); err == nil {
			t.Fatal("unknown field not rejected")
		}
	})
	t.Run("bad json rejected", func(t *testing.T) {
		if _, err := ParseFilter(`{bad`); err == nil {
			t.Fatal("bad json not rejected")
		}
	})
	t.Run("empty is zero", func(t *testing.T) {
		f, err := ParseFilter("")
		if err != nil || len(f.Regions) != 0 {
			t.Fatalf("empty filter = %+v err=%v", f, err)
		}
	})
}

func TestParseConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		c, err := ParseConfig(`{"remark_template":"{region} · {name} ({rtt}ms)","sort":"region","max_nodes":3,"include_rtt":false}`)
		if err != nil {
			t.Fatal(err)
		}
		if c.MaxNodes != 3 || c.Sort != "region" || c.RemarkTemplate == "" || c.IncludeRTT == nil || *c.IncludeRTT {
			t.Fatalf("parsed = %+v", c)
		}
	})
	t.Run("include_rtt absent defaults nil", func(t *testing.T) {
		c, err := ParseConfig(`{"max_nodes":1}`)
		if err != nil || c.IncludeRTT != nil {
			t.Fatalf("absent include_rtt = %+v err=%v", c, err)
		}
	})
	t.Run("unknown field rejected", func(t *testing.T) {
		if _, err := ParseConfig(`{"max_node":3}`); err == nil {
			t.Fatal("unknown config field not rejected")
		}
	})
	t.Run("bad json rejected", func(t *testing.T) {
		if _, err := ParseConfig(`{bad`); err == nil {
			t.Fatal("bad config json not rejected")
		}
	})
}

func names(entries []Entry) []string {
	out := make([]string, len(entries))
	for i := range entries {
		out[i] = entries[i].Node.Name
	}
	return out
}

// TestBuildRemarkLabels covers the {labels} placeholder (F-1107): the node's
// label list joins with commas, an unlabeled node substitutes the empty
// string, and bad label text is tolerated as empty (same tolerance as the
// filter side).
func TestBuildRemarkLabels(t *testing.T) {
	e := ent("hk1", "香港", `["iepl","vip"]`, "vless", 42)
	if got := BuildRemark(&e, Config{RemarkTemplate: "{labels} {name}"}); got != "iepl,vip hk1" {
		t.Fatalf("labels template = %q", got)
	}
	noLabel := ent("hk2", "香港", "", "vless", 42)
	if got := BuildRemark(&noLabel, Config{RemarkTemplate: "{labels}|{name}"}); got != "|hk2" {
		t.Fatalf("no labels = %q", got)
	}
	bad := ent("hk3", "香港", "not-json", "vless", 42)
	if got := BuildRemark(&bad, Config{RemarkTemplate: "{labels}-{name}"}); got != "-hk3" {
		t.Fatalf("bad labels tolerated = %q", got)
	}
}
