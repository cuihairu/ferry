package sub

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cuihairu/ferry/server/internal/protocol"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gopkg.in/yaml.v3"
)

// clashDoc is the packing structure (proxy groups / rules belong to the
// subscription document surface and are protocol-agnostic); the proxy entries
// come from the protocol registry hooks (a ClashProxy struct or a map), and the
// YAML order is guaranteed by the struct order.
type clashProxy = protocol.ClashProxy

type clashGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
	// The url-test group's self-optimizing fields (E-19); left empty for select groups.
	URL      string `yaml:"url,omitempty"`
	Interval int    `yaml:"interval,omitempty"`
	// Lazy defers the url-test probing until the group is actually selected:
	// mihomo's recommended default (G1), only the mihomo target sets it.
	Lazy bool `yaml:"lazy,omitempty"`
}

type clashDoc struct {
	Proxies     []any        `yaml:"proxies"`
	ProxyGroups []clashGroup `yaml:"proxy-groups"`
	Rules       []string     `yaml:"rules"`
}

// urlTestURL / urlTestInterval: the url-test group's connectivity probe and refresh interval.
const (
	urlTestURL      = "https://www.gstatic.com/generate_204"
	urlTestInterval = 300
)

// clashProxyName reads the proxy entry name (either the ClashProxy struct or the map form).
func clashProxyName(entry any) (string, error) {
	switch v := entry.(type) {
	case protocol.ClashProxy:
		return v.Name, nil
	case map[string]any:
		name, _ := v["name"].(string)
		return name, nil
	}
	return "", fmt.Errorf("clash proxy entry %T has no name", entry)
}

// PackClash generates the mihomo/clash subscription YAML (P0-5/E-19):
// region -> url-test group (self-optimizing inside the group), and a top-level
// PROXY select group aggregating the region groups. notes is remark text
// (TOUCH-7 outage resilience): each line is inserted as a YAML comment line at
// the head of the document, so the client cache carries its own escape hatch.
// PackClash packs with the conservative plain-clash vocabulary.
func PackClash(entries []Entry, notes []string) (string, error) {
	return packClash(entries, notes, false)
}

// PackMihomo is the Clash.Meta/mihomo subscription target (G1): the same
// proxies/proxy-groups/rules document as the clash target plus the meta-only
// extensions -- vless entries carry packet-encoding=xudp (UDP over vless, a
// Clash.Meta field plain clash loaders would not know) and the url-test groups
// are lazy (latency probing starts when the group is selected).
func PackMihomo(entries []Entry, notes []string) (string, error) {
	return packClash(entries, notes, true)
}

// packClash is the shared packing pipeline; meta selects the Clash.Meta
// extensions of PackMihomo.
func packClash(entries []Entry, notes []string, meta bool) (string, error) {
	doc := clashDoc{
		Proxies:     []any{},
		ProxyGroups: []clashGroup{{Name: "PROXY", Type: "select", Proxies: []string{}}},
		Rules:       []string{"MATCH,PROXY"},
	}
	regionGroup := map[string]int{}        // region -> index into proxy-groups
	used := map[string]bool{"PROXY": true} // names already taken (proxies and strategy groups share the namespace)
	claim := func(base string) string {
		name := base
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		used[name] = true
		return name
	}
	for i := range entries {
		e := &entries[i]
		entry, skip, err := clashEntryOf(&e.Node)
		if err != nil {
			return "", fmt.Errorf("node %s: %w", e.Node.Name, err)
		}
		if skip {
			continue
		}
		name, err := clashProxyName(entry)
		if err != nil {
			return "", fmt.Errorf("node %s: %w", e.Node.Name, err)
		}
		name = claim(name)
		// The name must be written back before the append: once the struct form is stored in an any slice it is no longer addressable.
		entry = withClashName(entry, name)
		if meta {
			entry = withMetaExtras(entry)
		}
		doc.Proxies = append(doc.Proxies, entry)

		region := entryRegion(e)
		gi, ok := regionGroup[region]
		if !ok {
			g := clashGroup{Name: claim(region), Type: "url-test", Proxies: []string{},
				URL: urlTestURL, Interval: urlTestInterval}
			if meta {
				g.Lazy = true
			}
			doc.ProxyGroups = append(doc.ProxyGroups, g)
			gi = len(doc.ProxyGroups) - 1
			regionGroup[region] = gi
			doc.ProxyGroups[0].Proxies = append(doc.ProxyGroups[0].Proxies, doc.ProxyGroups[gi].Name)
		}
		doc.ProxyGroups[gi].Proxies = append(doc.ProxyGroups[gi].Proxies, name)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, n := range notes {
		b.WriteString("# " + n + "\n")
	}
	b.Write(out)
	return b.String(), nil
}

// clashEntryOf renders the clash proxy entry through the protocol registry; ErrNotSupported returns skip.
func clashEntryOf(n *storage.Node) (entry any, skip bool, err error) {
	p, err := protocol.Lookup(n.Protocol)
	if err != nil {
		return nil, false, err
	}
	if p.RenderClash == nil {
		return nil, true, nil
	}
	rc, err := renderCtx(n, n.Name)
	if err != nil {
		return nil, false, err
	}
	if err := protocol.Validate(p, rc); err != nil {
		return nil, false, err
	}
	entry, err = p.RenderClash(rc)
	if errors.Is(err, protocol.ErrNotSupported) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	return entry, false, nil
}

// withClashName writes the de-duplicated name into the entry and returns the
// updated value (both forms). The struct form must be copied and returned: a
// struct stored in an any interface cannot be mutated in place.
func withClashName(entry any, name string) any {
	switch v := entry.(type) {
	case protocol.ClashProxy:
		v.Name = name
		return v
	case map[string]any:
		v["name"] = name
		return v
	}
	return entry
}

// withMetaExtras adds the Clash.Meta-only extensions (G1): packet-encoding for
// the vless entries. The value forms are handled the same way as withClashName
// (a struct stored in an any cannot be mutated in place).
func withMetaExtras(entry any) any {
	switch v := entry.(type) {
	case protocol.ClashProxy:
		if v.Type == "vless" {
			v.PacketEncoding = "xudp"
		}
		return v
	case map[string]any:
		if t, _ := v["type"].(string); t == "vless" {
			v["packet-encoding"] = "xudp"
		}
		return v
	}
	return entry
}
