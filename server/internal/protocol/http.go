package protocol

import (
	"net/url"
	"strconv"
)

// http proxy protocol definition (sing-box 1.14 outbound, verified by hand):
// either plain or TLS (the tls object is optional), credentials optional; the
// share link http://user:pass@host:port is the shape common to the v2rayN
// family; mihomo/clash support type: http. Extra field: username.
func init() {
	Register(Protocol{
		Name:        "http",
		KernelProc:  "sing-box",
		KernelKind:  "sing-box",
		DisplayName: "HTTP",
		DefaultPort: 8080,
		Fields: []Field{
			{Name: "username", Kind: KindString, Desc: "认证用户名"},
			{Name: "password", Kind: KindString, Desc: "认证密码"},
			{Name: "tls", Kind: KindBool, Desc: "启用 TLS"},
			{Name: "sni", Kind: KindString, Desc: "TLS SNI"},
			{Name: "insecure", Kind: KindBool, Desc: "跳过证书校验"},
		},
		RenderLink: func(rc *RenderCtx) (string, error) {
			cfg := rc.Config
			u := &url.URL{Scheme: "http", Host: rc.Node.Address + ":" + strconv.Itoa(rc.Node.Port)}
			if rc.Str("username") != "" || cfg.Password != "" {
				u.User = url.UserPassword(rc.Str("username"), cfg.Password)
			}
			return u.String() + "#" + url.PathEscape(rc.Remark), nil
		},
		RenderClash: func(rc *RenderCtx) (any, error) {
			cfg := rc.Config
			p := baseClashProxy(rc)
			p.Type = "http"
			p.Username = rc.Str("username")
			p.Password = cfg.Password
			p.TLS = cfg.TLS
			return p, nil
		},
		RenderSingbox: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			ob := baseSingboxOutbound(rc)
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
