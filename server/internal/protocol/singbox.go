package protocol

import (
	"fmt"
)

// Shared sing-box rendering pieces (moved in from sub/singbox.go, same
// conventions): the schema follows the officially documented sing-box
// outbound/inbound formats (plain JSON, no source import, so a binary
// adaptation does not infect us); real legality is gated per protocol by
// `sing-box check` (singbox_check_test.go; the verified findings with the real
// v1.14.4 binary are in each protocol file's comments).

// baseSingboxOutbound fills the common outbound fields (type/tag/server/server_port).
func baseSingboxOutbound(rc *RenderCtx) map[string]any {
	return map[string]any{
		"type":        rc.Node.Protocol,
		"tag":         rc.Node.Name,
		"server":      rc.Node.Address,
		"server_port": rc.Node.Port,
	}
}

// applySingBoxTLS writes the sing-box tls object (enabled is given by the
// caller per protocol convention: vless/vmess follow cfg.TLS, trojan and the
// QUIC family are always true); when not enabled no object is emitted (in
// sing-box server_name only makes sense under TLS); server_name takes sni and
// insecure passes through.
func applySingBoxTLS(ob map[string]any, cfg NodeConfig, enabled bool) {
	if !enabled {
		return
	}
	tls := map[string]any{"enabled": enabled}
	if cfg.SNI != "" {
		tls["server_name"] = cfg.SNI
	}
	if cfg.Insecure {
		tls["insecure"] = true
	}
	ob["tls"] = tls
}

// singBoxTransport writes the sing-box transport object per net: ws expands
// the same way as clash (path + Host header); grpc/http/httpupgrade cover the
// four common types; an unknown transport is reported explicitly (sing-box has
// no passthrough, so a wrong guess would only fail the client's config parse).
func singBoxTransport(cfg NodeConfig) (map[string]any, error) {
	switch cfg.Net {
	case "", "tcp":
		return nil, nil
	case "ws":
		t := map[string]any{"type": "ws"}
		if cfg.Path != "" {
			t["path"] = cfg.Path
		}
		if cfg.Host != "" {
			t["headers"] = map[string]any{"Host": cfg.Host}
		}
		return t, nil
	case "grpc":
		t := map[string]any{"type": "grpc"}
		if cfg.Path != "" {
			t["service_name"] = cfg.Path
		}
		return t, nil
	case "http":
		t := map[string]any{"type": "http"}
		if cfg.Host != "" {
			t["host"] = []any{cfg.Host}
		}
		if cfg.Path != "" {
			t["path"] = cfg.Path
		}
		return t, nil
	case "httpupgrade":
		t := map[string]any{"type": "httpupgrade"}
		if cfg.Host != "" {
			t["host"] = cfg.Host
		}
		if cfg.Path != "" {
			t["path"] = cfg.Path
		}
		return t, nil
	default:
		return nil, fmt.Errorf("unsupported net %q for sing-box transport", cfg.Net)
	}
}

// applyOutboundTransport wraps singBoxTransport: the transport key is only attached when non-nil.
func applyOutboundTransport(ob map[string]any, cfg NodeConfig) error {
	t, err := singBoxTransport(cfg)
	if err != nil {
		return err
	}
	if t != nil {
		ob["transport"] = t
	}
	return nil
}

// baseSingboxInbound fills the common inbound fields (type/tag/listen/listen_port).
func baseSingboxInbound(rc *RenderCtx, port int) map[string]any {
	listen := rc.Listen
	if listen == "" {
		listen = "127.0.0.1"
	}
	return map[string]any{
		"type":        rc.Node.Protocol,
		"tag":         "in-" + rc.Node.Protocol,
		"listen":      listen,
		"listen_port": port,
	}
}

// inboundTLS renders the inbound TLS object (required for the QUIC/TLS family):
// an absent certificate path means the caller has not prepared certificates, so it
// is reported explicitly rather than silenced (`sing-box check` really reads the
// certificate files for a TLS inbound -- init-level validation, the paths must
// exist).
func inboundTLS(rc *RenderCtx, sni string) (map[string]any, error) {
	if rc.CertPath == "" || rc.KeyPath == "" {
		return nil, fmt.Errorf("%s inbound tls requires cert/key paths", rc.Node.Protocol)
	}
	tls := map[string]any{
		"enabled":          true,
		"certificate_path": rc.CertPath,
		"key_path":         rc.KeyPath,
	}
	if sni != "" {
		tls["server_name"] = sni
	}
	return tls, nil
}
