// xray-json outbound JSON generation (G1, the Xray-core sibling of the sing-box
// format): it shares the same Entry source as clash/v2ray/sing-box and emits an
// Xray outbounds array fragment (which a subscription / client config can merge
// in directly). Convention: only shape the data (ferry does not render a full
// kernel config, the payload is pushed opaque); the schema follows the
// officially documented Xray-core outbound / streamSettings format (plain JSON,
// no source import, so a kernel adaptation does not infect us). A protocol whose
// hook returns ErrNotSupported (Xray has no outbound for hysteria2, tuic and
// wireguard) skips its entry; an unknown protocol is reported explicitly (never
// silently swallow a node, same convention as clash/v2ray).
package sub

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cuihairu/ferry/server/internal/protocol"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// XrayOutboundOf encodes a node as an Xray-core outbound object. A protocol that
// has no RenderXray hook (or one that returns ErrNotSupported) is reported
// explicitly -- single-point conversion has no skip semantics (only the
// subscription packing surface skips).
func XrayOutboundOf(n *storage.Node) (map[string]any, error) {
	p, err := protocol.Lookup(n.Protocol)
	if err != nil {
		return nil, err
	}
	rc, err := renderCtx(n, n.Name)
	if err != nil {
		return nil, err
	}
	if p.RenderXray == nil {
		return nil, fmt.Errorf("protocol %q: %w", p.Name, protocol.ErrNotSupported)
	}
	if err := protocol.Validate(p, rc); err != nil {
		return nil, err
	}
	ob, err := p.RenderXray(rc)
	if err != nil {
		return nil, err
	}
	return ob, nil
}

// PackXray returns the Xray-core outbounds JSON array of all entries (indented
// two spaces, usable as soon as the client merges it into its outbounds section).
func PackXray(entries []Entry) (string, error) {
	out := make([]map[string]any, 0, len(entries))
	for i := range entries {
		p, err := protocol.Lookup(entries[i].Node.Protocol)
		if err != nil {
			return "", fmt.Errorf("node %s: %w", entries[i].Node.Name, err)
		}
		if p.RenderXray == nil {
			continue // the protocol has no Xray outbound: skip (the subscription surface carries none)
		}
		rc, err := renderCtx(&entries[i].Node, entries[i].Node.Name)
		if err != nil {
			return "", fmt.Errorf("node %s: %w", entries[i].Node.Name, err)
		}
		if err := protocol.Validate(p, rc); err != nil {
			return "", fmt.Errorf("node %s: %w", entries[i].Node.Name, err)
		}
		ob, err := p.RenderXray(rc)
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
