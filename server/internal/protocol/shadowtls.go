package protocol

import (
	"errors"
)

// shadowtls v3 protocol definition (sing-box 1.14, verified by hand): a TLS
// disguise tunnel; the outbound needs version + password + a tls object, while
// the inbound form has no tls key (`check` reported unknown field "tls"), the
// certificates going through the handshake passthrough to the backend service.
// There is no industry-standard URI shape for a share link and mihomo's support
// is doubtful -- both are marked ErrNotSupported, annotated per kernel capability
// and per format (per the user's directive: "implement the three subscription
// format renders in full per each protocol's real fields, and annotate the
// unsupported formats").
func init() {
	Register(Protocol{
		Name:        "shadowtls",
		KernelProc:  "sing-box",
		KernelKind:  "sing-box",
		DisplayName: "ShadowTLS",
		DefaultPort: 443,
		Fields: []Field{
			{Name: "password", Kind: KindString, Required: true, Desc: "连接密码"},
			{Name: "version", Kind: KindInt, Default: 3, Desc: "协议版本（3）"},
			{Name: "sni", Kind: KindString, Desc: "TLS SNI"},
			{Name: "handshake_server", Kind: KindString, Desc: "入站握手透传目标主机"},
			{Name: "handshake_port", Kind: KindInt, Default: 8080, Desc: "入站握手透传目标端口"},
		},
		RenderClash: func(rc *RenderCtx) (any, error) {
			if rc.Config.Password == "" {
				return nil, errors.New("shadowtls requires config.password")
			}
			return nil, ErrNotSupported // mihomo's capability is doubtful; do not invent fields
		},
		RenderSingbox: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			if cfg.Password == "" {
				return nil, errors.New("shadowtls requires config.password")
			}
			ob := baseSingboxOutbound(rc)
			ob["version"] = orInt(rc.Int("version"), 3)
			ob["password"] = cfg.Password
			applySingBoxTLS(ob, cfg, true)
			return ob, nil
		},
		RenderSingboxInbound: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			in := baseSingboxInbound(rc, rc.Node.Port)
			in["version"] = orInt(rc.Int("version"), 3)
			in["users"] = []any{map[string]any{"password": cfg.Password}}
			hsServer := rc.Str("handshake_server")
			if hsServer == "" {
				return nil, errors.New("shadowtls inbound requires config.handshake_server")
			}
			in["handshake"] = map[string]any{
				"server":      hsServer,
				"server_port": orInt(rc.Int("handshake_port"), 8080),
			}
			return in, nil
		},
	})
}

// orInt is a zero-value fallback (the default convention for Extra integers).
func orInt(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}
