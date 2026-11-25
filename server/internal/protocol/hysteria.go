package protocol

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
)

// hysteria v1 protocol definition (sing-box 1.14 outbound, verified by hand):
// the QUIC/TLS family, and the auth field is a base64-encoded string in
// sing-box config (only discovered because `check` reported illegal base64 --
// copying the docs blindly would have broken it); the share link uses the
// official hysteria:// shape (auth/peer/upmbps/downmbps/obfs parameters);
// mihomo/clash meta support type: hysteria. Extra field: obfs (the obfuscation
// string).
func init() {
	Register(Protocol{
		Name:        "hysteria",
		KernelProc:  "sing-box",
		KernelKind:  "sing-box",
		DisplayName: "Hysteria (v1)",
		DefaultPort: 443,
		Fields: []Field{
			{Name: "password", Kind: KindString, Required: true, Desc: "认证密码（auth）"},
			{Name: "sni", Kind: KindString, Desc: "TLS SNI"},
			{Name: "insecure", Kind: KindBool, Desc: "跳过证书校验"},
			{Name: "up", Kind: KindInt, Desc: "上行带宽上限 Mbps"},
			{Name: "down", Kind: KindInt, Desc: "下行带宽上限 Mbps"},
			{Name: "obfs", Kind: KindString, Desc: "混淆字符串"},
		},
		RenderLink: func(rc *RenderCtx) (string, error) {
			cfg := rc.Config
			// hysteria v1 URI (the official client share-link convention):
			// hysteria://password@host:port?peer=&auth=&upmbps=&downmbps=&obfs=&insecure=1#remark
			if cfg.Password == "" {
				return "", errors.New("hysteria node requires config.password")
			}
			q := url.Values{}
			if cfg.SNI != "" {
				q.Set("peer", cfg.SNI)
			}
			q.Set("auth", cfg.Password)
			if cfg.Up > 0 {
				q.Set("upmbps", strconv.Itoa(cfg.Up))
			}
			if cfg.Down > 0 {
				q.Set("downmbps", strconv.Itoa(cfg.Down))
			}
			if obfs := rc.Str("obfs"); obfs != "" {
				q.Set("obfs", obfs)
			}
			if cfg.Insecure {
				q.Set("insecure", "1")
			}
			u := &url.URL{Scheme: "hysteria", Host: rc.Node.Address + ":" + strconv.Itoa(rc.Node.Port)}
			return u.String() + "?" + q.Encode() + "#" + url.PathEscape(rc.Remark), nil
		},
		RenderClash: func(rc *RenderCtx) (any, error) {
			cfg := rc.Config
			if cfg.Password == "" {
				return nil, errors.New("hysteria requires config.password")
			}
			p := baseClashProxy(rc)
			p.Type = "hysteria"
			p.Password = cfg.Password // the auth field of mihomo hysteria
			if cfg.Up > 0 {
				p.Up = cfg.Up
			}
			if cfg.Down > 0 {
				p.Down = cfg.Down
			}
			if obfs := rc.Str("obfs"); obfs != "" {
				p.Obfs = obfs
			}
			if cfg.Insecure {
				p.SkipCertVerify = true
			}
			return p, nil
		},
		RenderSingbox: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			if cfg.Password == "" {
				return nil, errors.New("hysteria requires config.password")
			}
			ob := baseSingboxOutbound(rc)
			// The auth of a sing-box hysteria outbound is a base64 string (verified with `check`).
			ob["auth"] = base64.StdEncoding.EncodeToString([]byte(cfg.Password))
			if cfg.Up > 0 {
				ob["up_mbps"] = cfg.Up
			}
			if cfg.Down > 0 {
				ob["down_mbps"] = cfg.Down
			}
			if obfs := rc.Str("obfs"); obfs != "" {
				ob["obfs"] = obfs
			}
			applySingBoxTLS(ob, cfg, true)
			return ob, nil
		},
		RenderSingboxInbound: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			in := baseSingboxInbound(rc, rc.Node.Port)
			if cfg.Up > 0 {
				in["up_mbps"] = cfg.Up
			}
			if cfg.Down > 0 {
				in["down_mbps"] = cfg.Down
			}
			in["users"] = []any{map[string]any{
				// The inbound users[].auth is likewise a base64 string (symmetric with the outbound).
				"auth": base64.StdEncoding.EncodeToString([]byte(cfg.Password)),
			}}
			tls, err := inboundTLS(rc, cfg.SNI)
			if err != nil {
				return nil, err
			}
			in["tls"] = tls
			return in, nil
		},
	})
}
