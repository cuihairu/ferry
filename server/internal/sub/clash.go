package sub

import (
	"fmt"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gopkg.in/yaml.v3"
)

// clashProxy 覆盖四种协议在 mihomo/clash 中的公共字段。
type clashProxy struct {
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
	// vless 用 servername，trojan/ss 用 sni，两者都填等值以省心。
	ServerName string `yaml:"servername,omitempty"`
	SNI        string `yaml:"sni,omitempty"`
	// trojan/ss
	Password string `yaml:"password,omitempty"`

	WSOpts *clashWSOpts `yaml:"ws-opts,omitempty"`
}

type clashWSOpts struct {
	Path    string            `yaml:"path,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

type clashGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
}

type clashDoc struct {
	Proxies     []clashProxy `yaml:"proxies"`
	ProxyGroups []clashGroup `yaml:"proxy-groups"`
	Rules       []string     `yaml:"rules"`
}

// PackClash 生成 mihomo/clash 订阅 YAML（P0-5）：全部节点进 PROXY 选择组。
func PackClash(nodes []storage.Node) (string, error) {
	doc := clashDoc{
		Proxies:     []clashProxy{},
		ProxyGroups: []clashGroup{{Name: "PROXY", Type: "select", Proxies: []string{}}},
		Rules:       []string{"MATCH,PROXY"},
	}
	seen := map[string]int{}
	for i := range nodes {
		p, err := clashProxyOf(&nodes[i])
		if err != nil {
			return "", fmt.Errorf("node %s: %w", nodes[i].Name, err)
		}
		// clash 内 name 是唯一键：重名追加序号避免互相覆盖。
		if n := seen[p.Name]; n > 0 {
			p.Name = fmt.Sprintf("%s-%d", p.Name, n+1)
		}
		seen[p.Name]++
		doc.Proxies = append(doc.Proxies, p)
		doc.ProxyGroups[0].Proxies = append(doc.ProxyGroups[0].Proxies, p.Name)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func clashProxyOf(n *storage.Node) (clashProxy, error) {
	cfg, err := parseConfig(n.Config)
	if err != nil {
		return clashProxy{}, err
	}
	p := clashProxy{Name: n.Name, Server: n.Address, Port: n.Port}
	switch n.Protocol {
	case "vless":
		if cfg.UUID == "" {
			return clashProxy{}, fmt.Errorf("vless requires config.uuid")
		}
		p.Type = "vless"
		p.UUID = cfg.UUID
		p.Flow = cfg.Flow
		p.TLS = cfg.TLS
	case "vmess":
		if cfg.UUID == "" {
			return clashProxy{}, fmt.Errorf("vmess requires config.uuid")
		}
		p.Type = "vmess"
		p.UUID = cfg.UUID
		p.AlterID = cfg.AID
		p.Cipher = defaultString(cfg.Scy, "auto")
		p.TLS = cfg.TLS
	case "trojan":
		if cfg.Password == "" {
			return clashProxy{}, fmt.Errorf("trojan requires config.password")
		}
		p.Type = "trojan"
		p.Password = cfg.Password
	case "shadowsocks":
		if cfg.Password == "" || cfg.Method == "" {
			return clashProxy{}, fmt.Errorf("shadowsocks requires config.method and config.password")
		}
		p.Type = "ss"
		p.Cipher = cfg.Method
		p.Password = cfg.Password
	default:
		return clashProxy{}, fmt.Errorf("unsupported protocol %q", n.Protocol)
	}
	if cfg.SNI != "" {
		p.ServerName = cfg.SNI
		p.SNI = cfg.SNI
	}
	applyTransport(&p, cfg)
	return p, nil
}

// applyTransport 按 net 写入传输层字段，P0 只展开 ws，其余（tcp/grpc…）透传 network。
func applyTransport(p *clashProxy, cfg nodeConfig) {
	if cfg.Net == "" || cfg.Net == "tcp" {
		return
	}
	p.Network = cfg.Net
	if cfg.Net == "ws" {
		p.WSOpts = &clashWSOpts{Path: cfg.Path}
		if cfg.Host != "" {
			p.WSOpts.Headers = map[string]string{"Host": cfg.Host}
		}
	}
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
