package protocol

import (
	"fmt"
)

// Shared Xray-core rendering pieces (the subscription xray-json format, G1): the
// schema follows the officially documented Xray-core outbound / streamSettings
// format (plain JSON, no source import, so a kernel adaptation does not infect
// us), so the four xray-family protocols only describe their own outbound
// schema and share the stream settings here. A protocol Xray has no outbound
// for (hysteria2, tuic, wireguard) leaves RenderXray unset, which is read as
// ErrNotSupported and the subscription packing surface skips its entry -- the
// same skip convention as the sing-box format.

// xrayOutbound fills the common outbound fields (tag + protocol name).
func xrayOutbound(rc *RenderCtx, proto string) map[string]any {
	return map[string]any{
		"tag":      rc.Node.Name,
		"protocol": proto,
	}
}

// xrayStream builds the Xray streamSettings object from the core config: the
// network defaults to tcp when cfg.Net is empty, the transport-specific settings
// key follows net, and the TLS object is emitted when enabled.
func xrayStream(rc *RenderCtx) (map[string]any, error) {
	return xrayStreamTLS(rc, rc.Config.TLS)
}

// xrayStreamTLS is xrayStream with the TLS decision given by the caller per
// protocol convention (trojan is TLS by its own semantics, so its hook forces
// it on even when cfg.TLS is not written); when not enabled no TLS object is
// emitted, since serverName only makes sense under TLS.
func xrayStreamTLS(rc *RenderCtx, enabled bool) (map[string]any, error) {
	cfg := rc.Config
	stream := map[string]any{
		"network": defaultString(cfg.Net, "tcp"),
	}
	if enabled {
		tls := map[string]any{}
		// serverName takes the sni and falls back to the node address (Xray's own
		// convention when the key is absent); both empty leaves it out.
		if cfg.SNI != "" {
			tls["serverName"] = cfg.SNI
		} else if rc.Node.Address != "" {
			tls["serverName"] = rc.Node.Address
		}
		if cfg.Insecure {
			tls["allowInsecure"] = true
		}
		stream["security"] = "tls"
		stream["tlsSettings"] = tls
	}
	switch cfg.Net {
	case "", "tcp":
		return stream, nil
	case "ws":
		ws := map[string]any{"path": cfg.Path}
		if cfg.Host != "" {
			ws["headers"] = map[string]any{"Host": cfg.Host}
		}
		stream["wsSettings"] = ws
		return stream, nil
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": cfg.Path}
		return stream, nil
	case "http":
		http := map[string]any{"path": cfg.Path}
		if cfg.Host != "" {
			http["host"] = []string{cfg.Host}
		}
		stream["httpSettings"] = http
		return stream, nil
	case "httpupgrade":
		hu := map[string]any{"path": cfg.Path}
		if cfg.Host != "" {
			hu["host"] = cfg.Host
		}
		stream["httpupgradeSettings"] = hu
		return stream, nil
	default:
		// Xray has no passthrough, so a wrong guess would only fail the client's
		// config parse: reported explicitly (same convention as singBoxTransport).
		return nil, fmt.Errorf("unsupported net %q for xray transport", cfg.Net)
	}
}
