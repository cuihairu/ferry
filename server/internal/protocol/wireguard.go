package protocol

import (
	"errors"
)

// wireguard protocol definition (sing-box 1.14, verified by hand): since 1.11 the
// wireguard outbound was removed (`check` reported unknown field "server") and
// endpoints took over -- the subscription singbox format is marked
// ErrNotSupported (the client subscription surface carries no endpoints), and the
// kernel config surface goes through RenderSingboxEndpoint. There is no standard
// URI for a share link; mihomo does support wireguard but its field surface is
// coupled to the kernel version, so nothing is invented and it is marked
// ErrNotSupported. Extra fields: local_address (a CIDR list), private_key,
// peer_public_key, allowed_ips, mtu, reserved.
func init() {
	Register(Protocol{
		Name:        "wireguard",
		KernelProc:  "wireguard",
		KernelKind:  "wireguard",
		DisplayName: "WireGuard",
		DefaultPort: 51820,
		Fields: []Field{
			{Name: "local_address", Kind: KindString, Required: true, Desc: "本端地址（CIDR，如 10.0.0.2/32）"},
			{Name: "private_key", Kind: KindString, Required: true, Group: "keys", Desc: "本端私钥（base64）"},
			{Name: "peer_public_key", Kind: KindString, Required: true, Group: "keys", Desc: "对端公钥（base64）"},
			{Name: "allowed_ips", Kind: KindString, Desc: "对端 allowed-ips（首个）"},
			{Name: "mtu", Kind: KindInt, Default: 1408, Desc: "MTU"},
			{Name: "reserved", Kind: KindString, Desc: "保留字节（逗号分隔，如 1,2,3）"},
		},
		RenderSingboxEndpoint: func(rc *RenderCtx) (map[string]any, error) {
			local := rc.Str("local_address")
			priv := rc.Str("private_key")
			peer := rc.Str("peer_public_key")
			if local == "" || priv == "" || peer == "" {
				return nil, errors.New("wireguard requires config.local_address, config.private_key, config.peer_public_key")
			}
			ep := map[string]any{
				"type":        "wireguard",
				"tag":         rc.Node.Name,
				"system":      false,
				"address":     []any{local},
				"private_key": priv,
			}
			peerObj := map[string]any{
				"public_key": peer,
				"address":    rc.Node.Address,
				"port":       rc.Node.Port,
			}
			if allowed := rc.Strs("allowed_ips"); len(allowed) > 0 {
				peerObj["allowed_ips"] = allowed
			}
			ep["peers"] = []any{peerObj}
			if mtu := rc.Int("mtu"); mtu > 0 {
				ep["mtu"] = mtu
			}
			return ep, nil
		},
	})
}
