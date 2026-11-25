package protocol

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
)

// shadowsocks protocol definition (migrated verbatim from the three sub
// formatters). method and password are a joint required group -- when either
// is missing it reports "requires config.method and config.password", which
// the side test matches with contains, so the compatibility holds.
func init() {
	Register(Protocol{
		Name:        "shadowsocks",
		KernelProc:  "xray",
		KernelKind:  "xray",
		DisplayName: "Shadowsocks",
		DefaultPort: 8388,
		Fields: []Field{
			{Name: "method", Kind: KindString, Required: true, Group: "crypto", Desc: "加密方法（如 aes-256-gcm）"},
			{Name: "password", Kind: KindString, Required: true, Group: "crypto", Desc: "连接密码"},
		},
		RenderLink: func(rc *RenderCtx) (string, error) {
			cfg := rc.Config
			if cfg.Password == "" || cfg.Method == "" {
				return "", errors.New("shadowsocks node requires config.method and config.password")
			}
			userinfo := base64.RawURLEncoding.EncodeToString([]byte(cfg.Method + ":" + cfg.Password))
			return "ss://" + userinfo + "@" + rc.Node.Address + ":" + strconv.Itoa(rc.Node.Port) + "#" + url.PathEscape(rc.Remark), nil
		},
		RenderClash: func(rc *RenderCtx) (any, error) {
			cfg := rc.Config
			if cfg.Password == "" || cfg.Method == "" {
				return nil, errors.New("shadowsocks requires config.method and config.password")
			}
			p := baseClashProxy(rc)
			p.Type = "ss"
			p.Cipher = cfg.Method
			p.Password = cfg.Password
			return p, nil
		},
		RenderSingbox: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			if cfg.Password == "" || cfg.Method == "" {
				return nil, errors.New("shadowsocks requires config.method and config.password")
			}
			ob := baseSingboxOutbound(rc)
			ob["method"] = cfg.Method
			ob["password"] = cfg.Password
			return ob, nil
		},
		RenderSingboxInbound: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			in := baseSingboxInbound(rc, rc.Node.Port)
			in["method"] = cfg.Method
			in["password"] = cfg.Password
			return in, nil
		},
	})
}
