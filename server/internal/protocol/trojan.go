package protocol

import (
	"errors"
	"net/url"
	"strconv"
)

// trojan protocol definition (migrated verbatim from the three sub formatters).
// The link-side query carries the common security-layer and transport-layer
// segments; the sing-box outbound TLS is always enabled (trojan is TLS itself).
func init() {
	Register(Protocol{
		Name:        "trojan",
		KernelProc:  "xray",
		KernelKind:  "xray",
		DisplayName: "Trojan",
		DefaultPort: 443,
		Fields: []Field{
			{Name: "password", Kind: KindString, Required: true, Desc: "连接密码"},
			{Name: "tls", Kind: KindBool, Desc: "启用 TLS（trojan 语义恒真，声明供表单展示）"},
			{Name: "sni", Kind: KindString, Desc: "TLS SNI"},
			{Name: "net", Kind: KindEnum, Enum: []string{"tcp", "ws", "grpc"}, Desc: "传输层"},
			{Name: "host", Kind: KindString, Desc: "ws Host 头"},
			{Name: "path", Kind: KindString, Desc: "ws/grpc 路径"},
		},
		RenderLink: func(rc *RenderCtx) (string, error) {
			cfg := rc.Config
			if cfg.Password == "" {
				return "", errors.New("trojan node requires config.password")
			}
			host := rc.Node.Address
			port := strconv.Itoa(rc.Node.Port)
			frag := url.PathEscape(rc.Remark)
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
			u := &url.URL{Scheme: "trojan", User: url.User(cfg.Password), Host: host + ":" + port}
			return u.String() + "?" + query.Encode() + "#" + frag, nil
		},
		RenderClash: func(rc *RenderCtx) (any, error) {
			cfg := rc.Config
			if cfg.Password == "" {
				return nil, errors.New("trojan requires config.password")
			}
			p := baseClashProxy(rc)
			p.Type = "trojan"
			p.Password = cfg.Password
			return p, nil
		},
		RenderSingbox: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			if cfg.Password == "" {
				return nil, errors.New("trojan requires config.password")
			}
			ob := baseSingboxOutbound(rc)
			ob["password"] = cfg.Password
			// trojan is TLS by its own semantics (trojan://), so tls.enabled is always true.
			applySingBoxTLS(ob, cfg, true)
			if err := applyOutboundTransport(ob, cfg); err != nil {
				return nil, err
			}
			return ob, nil
		},
		RenderSingboxInbound: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			in := baseSingboxInbound(rc, rc.Node.Port)
			in["users"] = []any{map[string]any{"password": cfg.Password}}
			tls, err := inboundTLS(rc, cfg.SNI)
			if err != nil {
				return nil, err
			}
			in["tls"] = tls
			return in, nil
		},
		RenderXray: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			if cfg.Password == "" {
				return nil, errors.New("trojan requires config.password")
			}
			// trojan is TLS by its own semantics (trojan://), so security tls is
			// emitted even when cfg.tls is not written -- the same decision as the
			// sing-box hook, which passes enabled=true.
			stream, err := xrayStreamTLS(rc, true)
			if err != nil {
				return nil, err
			}
			ob := xrayOutbound(rc, "trojan")
			ob["settings"] = map[string]any{
				"servers": []any{map[string]any{
					"address":  rc.Node.Address,
					"port":     rc.Node.Port,
					"password": cfg.Password,
				}},
			}
			ob["streamSettings"] = stream
			return ob, nil
		},
	})
}
