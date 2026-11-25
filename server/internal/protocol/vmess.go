package protocol

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
)

// vmess protocol definition (migrated verbatim from the three sub formatters).
func init() {
	Register(Protocol{
		Name:        "vmess",
		KernelProc:  "xray",
		KernelKind:  "xray",
		DisplayName: "VMess",
		DefaultPort: 443,
		Fields: []Field{
			{Name: "uuid", Kind: KindString, Required: true, Desc: "用户 UUID"},
			{Name: "scy", Kind: KindString, Default: "auto", Desc: "加密方式（缺省 auto）"},
			{Name: "aid", Kind: KindInt, Desc: "AlterID"},
			{Name: "tls", Kind: KindBool, Desc: "启用 TLS"},
			{Name: "sni", Kind: KindString, Desc: "TLS SNI"},
			{Name: "net", Kind: KindEnum, Enum: []string{"tcp", "ws", "grpc", "http", "httpupgrade"}, Default: "tcp", Desc: "传输层"},
			{Name: "host", Kind: KindString, Desc: "ws/http Host 头"},
			{Name: "path", Kind: KindString, Desc: "ws/http/grpc 路径"},
		},
		RenderLink: func(rc *RenderCtx) (string, error) {
			cfg := rc.Config
			if cfg.UUID == "" {
				return "", errors.New("vmess node requires config.uuid")
			}
			host := rc.Node.Address
			port := strconv.Itoa(rc.Node.Port)
			netType := cfg.Net
			if netType == "" {
				netType = "tcp"
			}
			scy := cfg.Scy
			if scy == "" {
				scy = "auto"
			}
			obj := map[string]string{
				"v": "2", "ps": rc.Remark, "add": host, "port": port, "id": cfg.UUID,
				"aid": strconv.Itoa(cfg.AID), "scy": scy, "net": netType, "type": "none",
				"host": cfg.Host, "path": cfg.Path,
			}
			if cfg.TLS {
				obj["tls"] = "tls"
				obj["sni"] = cfg.SNI
			}
			raw, err := json.Marshal(obj)
			if err != nil {
				return "", err
			}
			return "vmess://" + base64.StdEncoding.EncodeToString(raw), nil
		},
		RenderClash: func(rc *RenderCtx) (any, error) {
			cfg := rc.Config
			if cfg.UUID == "" {
				return nil, errors.New("vmess requires config.uuid")
			}
			p := baseClashProxy(rc)
			p.Type = "vmess"
			p.UUID = cfg.UUID
			p.AlterID = cfg.AID
			p.Cipher = defaultString(cfg.Scy, "auto")
			p.TLS = cfg.TLS
			return p, nil
		},
		RenderSingbox: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			if cfg.UUID == "" {
				return nil, errors.New("vmess requires config.uuid")
			}
			ob := baseSingboxOutbound(rc)
			ob["uuid"] = cfg.UUID
			ob["security"] = defaultString(cfg.Scy, "auto")
			ob["alter_id"] = cfg.AID
			applySingBoxTLS(ob, cfg, cfg.TLS)
			if err := applyOutboundTransport(ob, cfg); err != nil {
				return nil, err
			}
			return ob, nil
		},
		RenderSingboxInbound: func(rc *RenderCtx) (map[string]any, error) {
			cfg := rc.Config
			in := baseSingboxInbound(rc, rc.Node.Port)
			user := map[string]any{"uuid": cfg.UUID, "alterId": cfg.AID}
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

// defaultString is an empty-string fallback (moved in from sub, shared by several protocol hooks).
func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
