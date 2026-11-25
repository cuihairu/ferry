// sing-box outbound JSON generation (built in the hy2 batch, the five
// protocols completed in the SB batch; after the protocol pluginization refactor
// this package only assembles the array and prints it indented, the outbound
// objects come from the protocol registry hooks): it shares the same Entry
// source as clash/v2ray and emits a sing-box outbounds array fragment (which a
// subscription / client config can merge in directly). Convention: only shape
// the data (ferry does not render a full kernel config, the payload is pushed
// opaque); the schema follows the officially documented sing-box format (plain
// JSON, no source import, so a binary adaptation does not infect us); real
// legality is gated per protocol by `sing-box check`
// (internal/protocol/singbox_check_test.go). A protocol whose hook returns
// ErrNotSupported (the wireguard outbound form was removed by sing-box) skips
// its entry; an unknown protocol is reported explicitly (never silently swallow
// a node, same convention as clash/v2ray).
package sub

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cuihairu/ferry/server/internal/protocol"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// OutboundOf encodes a node as a sing-box outbound object. A protocol that
// returns ErrNotSupported is reported explicitly -- single-point conversion has
// no skip semantics (only the subscription packing surface skips).
func OutboundOf(n *storage.Node) (map[string]any, error) {
	p, err := protocol.Lookup(n.Protocol)
	if err != nil {
		return nil, err
	}
	rc, err := renderCtx(n, n.Name)
	if err != nil {
		return nil, err
	}
	if p.RenderSingbox == nil {
		return nil, fmt.Errorf("protocol %q: %w", p.Name, protocol.ErrNotSupported)
	}
	if err := protocol.Validate(p, rc); err != nil {
		return nil, err
	}
	ob, err := p.RenderSingbox(rc)
	if err != nil {
		return nil, err
	}
	return ob, nil
}

// PackSingBox returns the sing-box outbounds JSON array of all entries (indented
// two spaces, usable as soon as the client merges it into its outbounds section).
func PackSingBox(entries []Entry) (string, error) {
	out := make([]map[string]any, 0, len(entries))
	for i := range entries {
		p, err := protocol.Lookup(entries[i].Node.Protocol)
		if err != nil {
			return "", fmt.Errorf("node %s: %w", entries[i].Node.Name, err)
		}
		if p.RenderSingbox == nil {
			continue // the protocol has no outbound form: skip (the subscription surface carries none)
		}
		rc, err := renderCtx(&entries[i].Node, entries[i].Node.Name)
		if err != nil {
			return "", fmt.Errorf("node %s: %w", entries[i].Node.Name, err)
		}
		if err := protocol.Validate(p, rc); err != nil {
			return "", fmt.Errorf("node %s: %w", entries[i].Node.Name, err)
		}
		ob, err := p.RenderSingbox(rc)
		if errors.Is(err, protocol.ErrNotSupported) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("node %s: %w", entries[i].Node.Name, err)
		}
		out = append(out, ob)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
