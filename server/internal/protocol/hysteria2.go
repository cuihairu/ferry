package protocol

import (
	"errors"
	"net/url"
	"strconv"
)

// hysteria2 protocol definition (migrated verbatim from the three sub
// formatters): the URI and the obfs/up/down fields follow the hysteria2 client
// conventions, with up/down in Mbps.
func init() {
	Register(Protocol{
		Name:        "hysteria2",
		KernelProc:  "hysteria2",
		KernelKind:  "hysteria2",
		DisplayName: "Hysteria2 (hy2)",
		DefaultPort: 443,
		Fields: []Field{
			{Name: "password", Kind: KindString, Required: true, Desc: "认证密码"},
			{Name: "sni", Kind: KindString, Desc: "TLS SNI"},
			{Name: "obfs", Kind: KindString, Enum: []string{"salamander"}, Desc: "混淆类型"},
			{Name: "obfs_password", Kind: KindString, Desc: "混淆密码"},
			{Name: "insecure", Kind: KindBool, Desc: "跳过证书校验（自签场景）"},
			{Name: "up", Kind: KindInt, Desc: "上行带宽上限 Mbps"},
			{Name: "down", Kind: KindInt, Desc: "下行带宽上限 Mbps"},
		},
		RenderLink: func(rc *RenderCtx) (string, error) {
			cfg := rc.Config
			// hysteria2 URI (the Hysteria2 client / share-link convention):
			// hysteria2://password@host:port?sni=&obfs=&obfs-password=&insecure=&up=&down=#remark
			if cfg.Password == "" {
				return "", errors.New("hysteria2 node requires config.password")
			}
			host := rc.Node.Address
			port := strconv.Itoa(rc.Node.Port)
			frag := url.PathEscape(rc.Remark)
			q := url.Values{}
			if cfg.SNI != "" {
				q.Set("sni", cfg.SNI)
			}
			if cfg.Obfs != "" {
				q.Set("obfs", cfg.Obfs)
				if cfg.ObfsPassword != "" {
					q.Set("obfs-password", cfg.ObfsPassword)
				}
			}
			if cfg.Insecure {
				q.Set("insecure", "1")
			}
			if cfg.Up > 0 {
				q.Set("up", strconv.Itoa(cfg.Up))
			}
			if cfg.Down > 0 {
				q.Set("down", strconv.Itoa(cfg.Down))
			}
			u := &url.URL{Scheme: "hysteria2", User: url.User(cfg.Password), Host: host + ":" + port}
			return u.String() + "?" + q.Encode() + "#" + frag, nil
		},
		RenderClash: func(rc *RenderCtx) (any, error) {
			cfg := rc.Config
			if cfg.Password == "" {
				return nil, errors.New("hysteria2 requires config.password")
			}
			p := baseClashProxy(rc)
			p.Type = "hysteria2"
			p.Password = cfg.Password
			if cfg.Obfs != "" {
				p.Obfs = cfg.Obfs
				p.ObfsPassword = cfg.ObfsPassword
			}
			if cfg.Up > 0 {
				p.Up = cfg.Up
			}
			if cfg.Down > 0 {
				p.Down = cfg.Down
			}
			if cfg.Insecure {
				p.SkipCertVerify = true
			}
			return p, nil
		},
		RenderSingbox: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			if cfg.Password == "" {
				return nil, errors.New("hysteria2 requires config.password")
			}
			ob := baseSingboxOutbound(rc)
			ob["password"] = cfg.Password
			if cfg.SNI != "" {
				ob["sni"] = cfg.SNI
			}
			if cfg.Insecure {
				ob["insecure"] = true
			}
			if cfg.Obfs != "" {
				obfs := map[string]any{"type": cfg.Obfs}
				if cfg.ObfsPassword != "" {
					obfs["password"] = cfg.ObfsPassword
				}
				ob["obfs"] = obfs
			}
			if cfg.Up > 0 {
				ob["up_mbps"] = cfg.Up
			}
			if cfg.Down > 0 {
				ob["down_mbps"] = cfg.Down
			}
			return ob, nil
		},
		RenderSingboxInbound: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			in := baseSingboxInbound(rc, rc.Node.Port)
			in["users"] = []any{map[string]any{"password": cfg.Password}}
			if cfg.Up > 0 {
				in["up_mbps"] = cfg.Up
			}
			if cfg.Down > 0 {
				in["down_mbps"] = cfg.Down
			}
			if cfg.Obfs != "" {
				in["obfs"] = map[string]any{"type": cfg.Obfs, "password": cfg.ObfsPassword}
			}
			tls, err := inboundTLS(rc, cfg.SNI)
			if err != nil {
				return nil, err
			}
			in["tls"] = tls
			return in, nil
		},
	})
}
