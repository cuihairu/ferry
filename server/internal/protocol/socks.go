package protocol

import (
	"net/url"
	"strconv"
)

// socks5 proxy protocol definition (sing-box 1.14 outbound, verified by hand:
// version is required as "5"): credentials are optional; the share link
// socks://user:pass@host:port is the shape common to the v2rayN family;
// mihomo/clash support type: socks5. Extra field: username.
func init() {
	Register(Protocol{
		Name:        "socks",
		KernelProc:  "sing-box",
		KernelKind:  "sing-box",
		DisplayName: "SOCKS5",
		DefaultPort: 1080,
		Fields: []Field{
			{Name: "username", Kind: KindString, Desc: "认证用户名"},
			{Name: "password", Kind: KindString, Desc: "认证密码"},
			{Name: "tls", Kind: KindBool, Desc: "启用 TLS"},
		},
		RenderLink: func(rc *RenderCtx) (string, error) {
			cfg := rc.Config
			u := &url.URL{Scheme: "socks", Host: rc.Node.Address + ":" + strconv.Itoa(rc.Node.Port)}
			if rc.Str("username") != "" || cfg.Password != "" {
				u.User = url.UserPassword(rc.Str("username"), cfg.Password)
			}
			return u.String() + "#" + url.PathEscape(rc.Remark), nil
		},
		RenderClash: func(rc *RenderCtx) (any, error) {
			cfg := rc.Config
			p := baseClashProxy(rc)
			p.Type = "socks5"
			p.Username = rc.Str("username")
			p.Password = cfg.Password
			p.TLS = cfg.TLS
			return p, nil
		},
		RenderSingbox: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			ob := baseSingboxOutbound(rc)
			ob["version"] = "5"
			if u := rc.Str("username"); u != "" {
				ob["username"] = u
			}
			if cfg.Password != "" {
				ob["password"] = cfg.Password
			}
			applySingBoxTLS(ob, cfg, cfg.TLS)
			return ob, nil
		},
		RenderSingboxInbound: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			in := baseSingboxInbound(rc, rc.Node.Port)
			if rc.Str("username") != "" || cfg.Password != "" {
				in["users"] = []any{map[string]any{"username": rc.Str("username"), "password": cfg.Password}}
			}
			return in, nil
		},
	})
}
