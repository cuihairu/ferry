package protocol

import (
	"errors"
	"net/url"
	"strconv"
)

// tuic v5 protocol definition (sing-box 1.14 outbound, verified by hand): the
// QUIC/TLS family with a uuid + password dual credential; 1.14's outbound no
// longer has an alpn field (`check` reported unknown field, alpn is negotiated
// by the QUIC layer), and congestion_control defaults to bbr. The share link
// uses the tuic://uuid:password@host:port shape; mihomo/clash meta support
// type: tuic.
func init() {
	Register(Protocol{
		Name:        "tuic",
		KernelProc:  "sing-box",
		KernelKind:  "sing-box",
		DisplayName: "TUIC",
		DefaultPort: 443,
		Fields: []Field{
			{Name: "uuid", Kind: KindString, Required: true, Group: "cred", Desc: "用户 UUID"},
			{Name: "password", Kind: KindString, Required: true, Group: "cred", Desc: "认证密码"},
			{Name: "sni", Kind: KindString, Desc: "TLS SNI"},
			{Name: "insecure", Kind: KindBool, Desc: "跳过证书校验"},
			{Name: "congestion_control", Kind: KindEnum, Enum: []string{"bbr", "brutal"}, Default: "bbr", Desc: "拥塞控制"},
		},
		RenderLink: func(rc *RenderCtx) (string, error) {
			cfg := rc.Config
			if cfg.UUID == "" || cfg.Password == "" {
				return "", errors.New("tuic requires config.uuid and config.password")
			}
			q := url.Values{}
			if cfg.SNI != "" {
				q.Set("sni", cfg.SNI)
			}
			cc := rc.Str("congestion_control")
			if cc == "" {
				cc = "bbr"
			}
			q.Set("congestion_control", cc)
			if cfg.Insecure {
				q.Set("allow_insecure", "1")
			}
			u := &url.URL{
				Scheme: "tuic",
				User:   url.UserPassword(cfg.UUID, cfg.Password),
				Host:   rc.Node.Address + ":" + strconv.Itoa(rc.Node.Port),
			}
			return u.String() + "?" + q.Encode() + "#" + url.PathEscape(rc.Remark), nil
		},
		RenderClash: func(rc *RenderCtx) (any, error) {
			cfg := rc.Config
			if cfg.UUID == "" || cfg.Password == "" {
				return nil, errors.New("tuic requires config.uuid and config.password")
			}
			p := baseClashProxy(rc)
			p.Type = "tuic"
			p.UUID = cfg.UUID
			p.Password = cfg.Password
			if cfg.Insecure {
				p.SkipCertVerify = true
			}
			cc := rc.Str("congestion_control")
			if cc == "" {
				cc = "bbr"
			}
			return map[string]any{
				// The legacy ClashProxy struct carries no tuic-specific field
				// (congestion-controller), so tuic renders as a map; the common fields
				// keep the same names and values as baseClashProxy.
				"name": p.Name, "type": p.Type, "server": p.Server, "port": p.Port,
				"uuid": p.UUID, "password": p.Password,
				"congestion-controller": cc,
				"sni":                   cfg.SNI,
				"skip-cert-verify":      cfg.Insecure,
			}, nil
		},
		RenderSingbox: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			if cfg.UUID == "" || cfg.Password == "" {
				return nil, errors.New("tuic requires config.uuid and config.password")
			}
			ob := baseSingboxOutbound(rc)
			ob["uuid"] = cfg.UUID
			ob["password"] = cfg.Password
			cc := rc.Str("congestion_control")
			if cc == "" {
				cc = "bbr"
			}
			ob["congestion_control"] = cc
			applySingBoxTLS(ob, cfg, true)
			return ob, nil
		},
		RenderSingboxInbound: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			in := baseSingboxInbound(rc, rc.Node.Port)
			in["users"] = []any{map[string]any{"uuid": cfg.UUID, "password": cfg.Password}}
			cc := rc.Str("congestion_control")
			if cc == "" {
				cc = "bbr"
			}
			in["congestion_control"] = cc
			tls, err := inboundTLS(rc, cfg.SNI)
			if err != nil {
				return nil, err
			}
			in["tls"] = tls
			return in, nil
		},
	})
}
