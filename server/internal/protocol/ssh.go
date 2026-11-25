package protocol

// ssh proxy protocol definition (sing-box 1.14 outbound, verified by hand):
// password or private_key, one of the two (no joint required declaration --
// either one being present is legal); ssh has no inbound form (a
// client-only protocol) and no standard URI for a share link, both marked
// ErrNotSupported. Extra fields: username, private_key,
// private_key_passphrase.
func init() {
	Register(Protocol{
		Name:        "ssh",
		KernelProc:  "sing-box",
		KernelKind:  "sing-box",
		DisplayName: "SSH",
		DefaultPort: 22,
		Fields: []Field{
			{Name: "username", Kind: KindString, Default: "root", Desc: "登录用户"},
			{Name: "password", Kind: KindString, Desc: "登录密码（与 private_key 二选一）"},
			{Name: "private_key", Kind: KindString, Desc: "私钥 PEM（与 password 二选一）"},
			{Name: "private_key_passphrase", Kind: KindString, Desc: "私钥口令"},
		},
		RenderSingbox: func(rc *RenderCtx) (map[string]any, error) {
			ob := baseSingboxOutbound(rc)
			if u := rc.Str("username"); u != "" {
				ob["user"] = u
			}
			if pw := rc.Config.Password; pw != "" {
				ob["password"] = pw
			}
			if key := rc.Str("private_key"); key != "" {
				ob["private_key"] = key
			}
			if pass := rc.Str("private_key_passphrase"); pass != "" {
				ob["private_key_passphrase"] = pass
			}
			return ob, nil
		},
	})
}
