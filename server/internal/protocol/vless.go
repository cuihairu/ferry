package protocol

import (
	"errors"
	"net/url"
	"strconv"
)

// vless protocol definition (migrated verbatim from sub/links.go, sub/clash.go
// and sub/singbox.go): the rendering logic is identical to the pre-refactor
// code, and the output is gated by the golden comparison test
// (sub/testdata/golden.json).
func init() {
	Register(Protocol{
		Name:        "vless",
		KernelProc:  "xray",
		KernelKind:  "xray",
		DisplayName: "VLESS",
		DefaultPort: 443,
		Fields: []Field{
			{Name: "uuid", Kind: KindString, Required: true, Desc: "用户 UUID"},
			{Name: "flow", Kind: KindEnum, Enum: []string{"xtls-rprx-vision"}, Desc: "xTLS 流控（vision）"},
			{Name: "tls", Kind: KindBool, Desc: "启用 TLS"},
			{Name: "sni", Kind: KindString, Desc: "TLS SNI"},
			{Name: "net", Kind: KindEnum, Enum: []string{"tcp", "ws", "grpc", "http", "httpupgrade"}, Desc: "传输层"},
			{Name: "host", Kind: KindString, Desc: "ws/http Host 头"},
			{Name: "path", Kind: KindString, Desc: "ws/http/grpc 路径"},
		},
		RenderLink: func(rc *RenderCtx) (string, error) {
			cfg := rc.Config
			if cfg.UUID == "" {
				return "", errors.New("vless node requires config.uuid")
			}
			host := rc.Node.Address
			query := url.Values{}
			if cfg.TLS {
				query.Set("security", "tls")
				if cfg.SNI != "" {
					query.Set("sni", cfg.SNI)
				}
			}
			if cfg.Net != "" {
				query.Set("type", cfg.Net)
			}
			if cfg.Host != "" {
				query.Set("host", cfg.Host)
			}
			if cfg.Path != "" {
				query.Set("path", cfg.Path)
			}
			q := url.Values{}
			q.Set("encryption", "none")
			if cfg.Flow != "" {
				q.Set("flow", cfg.Flow)
			}
			for k, v := range query {
				q[k] = v
			}
			return "vless://" + cfg.UUID + "@" + host + ":" + strconv.Itoa(rc.Node.Port) + "?" + q.Encode() + "#" + url.PathEscape(rc.Remark), nil
		},
		RenderClash: func(rc *RenderCtx) (any, error) {
			cfg := rc.Config
			if cfg.UUID == "" {
				return nil, errors.New("vless requires config.uuid")
			}
			p := baseClashProxy(rc)
			p.Type = "vless"
			p.UUID = cfg.UUID
			p.Flow = cfg.Flow
			p.TLS = cfg.TLS
			return p, nil
		},
		RenderSingbox: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			if cfg.UUID == "" {
				return nil, errors.New("vless requires config.uuid")
			}
			ob := baseSingboxOutbound(rc)
			ob["uuid"] = cfg.UUID
			if cfg.Flow != "" {
				ob["flow"] = cfg.Flow
			}
			applySingBoxTLS(ob, cfg, cfg.TLS)
			if err := applyOutboundTransport(ob, cfg); err != nil {
				return nil, err
			}
			return ob, nil
		},
		RenderSingboxInbound: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			in := baseSingboxInbound(rc, rc.Node.Port)
			user := map[string]any{"uuid": cfg.UUID}
			if cfg.Flow != "" {
				user["flow"] = cfg.Flow
			}
			in["users"] = []any{user}
			if cfg.TLS {
				tls, err := inboundTLS(rc, cfg.SNI)
				if err != nil {
					return nil, err
				}
				in["tls"] = tls
			}
			return in, nil
		},
	})
}
