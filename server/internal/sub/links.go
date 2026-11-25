// Package sub generates subscription content: v2ray share links and
// clash/mihomo YAML (P0-4/P0-5). Protocol pluginization refactor (2026-11-24):
// this package only assembles data (grouping / sorting / remarks / wrapping),
// and all protocol knowledge lives in the internal/protocol registry -- the
// formatters walk the entries and render them through the registry hooks, skip
// the entry for a protocol whose hook returns ErrNotSupported (the skip
// convention), and report an explicit error for the whole pack when the protocol
// is unknown (a Lookup miss), same as before the refactor.
package sub

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/protocol"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// nodeConfig / parseConfig are package-local aliases for the protocol package's
// core config (legacy tests and call sites keep the old names); the real
// definition has moved to protocol.NodeConfig.
type nodeConfig = protocol.NodeConfig

func parseConfig(raw string) (nodeConfig, error) {
	return protocol.ParseConfig(raw)
}

// renderCtx assembles the protocol render input: core config + free fields + remark.
func renderCtx(n *storage.Node, remark string) (*protocol.RenderCtx, error) {
	cfg, err := protocol.ParseConfig(n.Config)
	if err != nil {
		return nil, err
	}
	extra, err := protocol.ParseExtra(n.Config)
	if err != nil {
		return nil, err
	}
	return &protocol.RenderCtx{Node: *n, Config: cfg, Extra: extra, Remark: remark}, nil
}

// ShareLink encodes a node as its protocol's share link (P0-4). A protocol that
// returns ErrNotSupported (no link form, e.g. wireguard) is reported explicitly
// -- single-point sharing has no skip semantics.
func ShareLink(n *storage.Node) (string, error) {
	return shareLink(n, n.Name)
}

// shareLink is ShareLink with the remark given explicitly (the subscription side carries the region and the latency, E-19).
func shareLink(n *storage.Node, remark string) (string, error) {
	p, err := protocol.Lookup(n.Protocol)
	if err != nil {
		return "", err
	}
	rc, err := renderCtx(n, remark)
	if err != nil {
		return "", err
	}
	if p.RenderLink == nil {
		return "", fmt.Errorf("protocol %q: %w", p.Name, protocol.ErrNotSupported)
	}
	if err := protocol.Validate(p, rc); err != nil {
		return "", err
	}
	return p.RenderLink(rc)
}

// Entry is one entry handed to the client: the node itself + the speedtest
// latency aggregated by the panel (E-19). RttMs is the recent aggregation of the
// edge probe verdicts, and 0 means no data yet. A non-empty Remark overrides the
// default remark (per-sub remark_template, ferry-sub-filters); when empty,
// entryRemark is used instead (region · name (latency ms)) -- the main
// subscription path is unaffected.
type Entry struct {
	Node  storage.Node
	RttMs int
	// Remark is the override remark pre-generated on the distribution side (see sub/filter.go ApplyConfig).
	Remark string
}

// entryRegion is the region name for display: an empty value falls back to the
// unknown-region label returned below.
func entryRegion(e *Entry) string {
	if e.Node.Region == "" {
		return "未知"
	}
	return e.Node.Region
}

// entryRemark: v2ray has no real grouping, so grouping is expressed through
// sorting + the remark: "region · name (latency ms)", with the parenthetical
// omitted when there is no speedtest data.
func entryRemark(e *Entry) string {
	region := entryRegion(e)
	if e.RttMs > 0 {
		return fmt.Sprintf("%s · %s (%dms)", region, e.Node.Name, e.RttMs)
	}
	return fmt.Sprintf("%s · %s", region, e.Node.Name)
}

// entryLink generates the share link with a remark (protocol-internal fields:
// the URI fragment / vmess ps). A non-empty Remark is used as is (per-sub remark
// template), otherwise the default region · name remark. A protocol with no link
// form (ErrNotSupported) returns the skip signal -- the packing side then skips
// the entry per the skip convention instead of dragging the whole pack down.
func entryLink(e *Entry) (link string, skip bool, err error) {
	remark := e.Remark
	if remark == "" {
		remark = entryRemark(e)
	}
	link, err = shareLink(&e.Node, remark)
	if errors.Is(err, protocol.ErrNotSupported) {
		return "", true, nil
	}
	return link, false, err
}

// PackV2Ray returns the base64 of the link list sorted by region grouping
// (the v2ray client subscription format, E-19). notes is remark text (TOUCH-7
// outage resilience): each line is inserted as a "# " comment line at the head
// of the decoded text, and clients ignore non-link lines when parsing line by
// line -- the subscription cache carries its own escape hatch. An empty list
// still emits the remarks: with no available node the subscription can be
// refreshed, and the user keeps the backup entries and the announcement address.
func PackV2Ray(entries []Entry, notes []string) (string, error) {
	return PackV2RayOrdered(orderByProbe(entries, time.Now()), notes)
}

// PackV2RayOrdered is the same as PackV2Ray but keeps the order given by the
// caller without re-sorting (the per-sub sort convention, ferry-sub-filters):
// the main subscription uses PackV2Ray's region grouping sort, while a
// self-built subscription is already sorted per its per-sub sort inside
// sub.ApplyConfig. PackV2Ray reuses it, so the main subscription output is unchanged.
func PackV2RayOrdered(entries []Entry, notes []string) (string, error) {
	lines := make([]string, 0, len(notes)+len(entries))
	for _, n := range notes {
		lines = append(lines, "# "+n)
	}
	for i := range entries {
		link, skip, err := entryLink(&entries[i])
		if err != nil {
			return "", fmt.Errorf("node %s: %w", entries[i].Node.Name, err)
		}
		if skip {
			continue
		}
		lines = append(lines, link)
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n"))), nil
}
