package protocol

// Shared clash rendering pieces (moved in from sub/clash.go): the proxy entry
// struct keeps its field order and omitempty convention unchanged -- that is
// what keeps the YAML output of the five legacy protocols byte-compatible.
// New protocols may return map[string]any from their render (mihomo renders
// it key-value), with no order constraint.

// ClashProxy covers the common fields of the legacy protocols in mihomo/clash.
type ClashProxy struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	Server  string `yaml:"server"`
	Port    int    `yaml:"port"`
	Network string `yaml:"network,omitempty"`
	// vless/vmess
	UUID    string `yaml:"uuid,omitempty"`
	AlterID int    `yaml:"alterId,omitempty"`
	Cipher  string `yaml:"cipher,omitempty"`
	Flow    string `yaml:"flow,omitempty"`
	TLS     bool   `yaml:"tls,omitempty"`
	// vless uses servername, trojan/ss use sni; both are filled with the same
	// value to keep things simple.
	ServerName string `yaml:"servername,omitempty"`
	SNI        string `yaml:"sni,omitempty"`
	// trojan/ss
	Password string `yaml:"password,omitempty"`

	// hysteria2 (mihomo/clash meta style): up/down are in Mbps; obfs defaults
	// to salamander; skip-cert-verify matches the self-signed certificate case.
	Obfs           string   `yaml:"obfs,omitempty"`
	ObfsPassword   string   `yaml:"obfs-password,omitempty"`
	Up             int      `yaml:"up,omitempty"`
	Down           int      `yaml:"down,omitempty"`
	SkipCertVerify bool     `yaml:"skip-cert-verify,omitempty"`
	Alpn           []string `yaml:"alpn,omitempty"`

	WSOpts *ClashWSOpts `yaml:"ws-opts,omitempty"`

	// Username serves http/socks-style proxies (new protocols; the field is appended so the legacy order stays untouched).
	Username string `yaml:"username,omitempty"`
}

// ClashWSOpts is the expanded struct for the ws transport layer.
type ClashWSOpts struct {
	Path    string            `yaml:"path,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

// applyClashTransport writes the transport-layer fields per net; P0 only expands
// ws, the rest (tcp/grpc...) pass network through (moved in from sub, same
// convention).
func applyClashTransport(p *ClashProxy, cfg NodeConfig) {
	if cfg.Net == "" || cfg.Net == "tcp" {
		return
	}
	p.Network = cfg.Net
	if cfg.Net == "ws" {
		p.WSOpts = &ClashWSOpts{Path: cfg.Path}
		if cfg.Host != "" {
			p.WSOpts.Headers = map[string]string{"Host": cfg.Host}
		}
	}
}

// baseClashProxy fills the common fields (name/server/port + the sni double
// write + the transport layer); the protocol hooks add type and their
// protocol-specific fields.
func baseClashProxy(rc *RenderCtx) ClashProxy {
	p := ClashProxy{Name: rc.Node.Name, Server: rc.Node.Address, Port: rc.Node.Port}
	if rc.Config.SNI != "" {
		p.ServerName = rc.Config.SNI
		p.SNI = rc.Config.SNI
	}
	applyClashTransport(&p, rc.Config)
	return p
}
