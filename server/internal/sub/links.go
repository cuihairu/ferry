// Package sub 生成订阅内容：v2ray 分享链接与 clash/mihomo YAML（P0-4/P0-5）。
package sub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// nodeConfig 是节点配置模板的约定形态（协议无关，缺省按协议补）。
// {"uuid","password","method","tls","sni","host","path","net","aid","scy","flow"}
type nodeConfig struct {
	UUID     string `json:"uuid"`
	Password string `json:"password"`
	Method   string `json:"method"`
	TLS      bool   `json:"tls"`
	SNI      string `json:"sni"`
	Host     string `json:"host"`
	Path     string `json:"path"`
	Net      string `json:"net"`
	AID      int    `json:"aid"`
	Scy      string `json:"scy"`
	Flow     string `json:"flow"`
}

func parseConfig(raw string) (nodeConfig, error) {
	var cfg nodeConfig
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(trimmed), &cfg); err != nil {
		return cfg, fmt.Errorf("parse node config: %w", err)
	}
	return cfg, nil
}

// ShareLink 把节点编码为对应协议的分享链接（P0-4）。
func ShareLink(n *storage.Node) (string, error) {
	cfg, err := parseConfig(n.Config)
	if err != nil {
		return "", err
	}
	host := n.Address
	port := strconv.Itoa(n.Port)
	frag := url.PathEscape(n.Name)
	query := url.Values{}
	if cfg.TLS {
		query.Set("security", "tls")
		if cfg.SNI != "" {
			query.Set("sni", cfg.SNI)
		}
	}
	if cfg.Net != "" {
		query.Set("type", cfg.Net)
	}
	if cfg.Host != "" {
		query.Set("host", cfg.Host)
	}
	if cfg.Path != "" {
		query.Set("path", cfg.Path)
	}

	switch n.Protocol {
	case "vless":
		if cfg.UUID == "" {
			return "", errors.New("vless node requires config.uuid")
		}
		q := url.Values{}
		q.Set("encryption", "none")
		if cfg.Flow != "" {
			q.Set("flow", cfg.Flow)
		}
		for k, v := range query {
			q[k] = v
		}
		return "vless://" + cfg.UUID + "@" + host + ":" + port + "?" + q.Encode() + "#" + frag, nil
	case "vmess":
		if cfg.UUID == "" {
			return "", errors.New("vmess node requires config.uuid")
		}
		netType := cfg.Net
		if netType == "" {
			netType = "tcp"
		}
		scy := cfg.Scy
		if scy == "" {
			scy = "auto"
		}
		obj := map[string]string{
			"v": "2", "ps": n.Name, "add": host, "port": port, "id": cfg.UUID,
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
	case "trojan":
		if cfg.Password == "" {
			return "", errors.New("trojan node requires config.password")
		}
		u := &url.URL{Scheme: "trojan", User: url.User(cfg.Password), Host: host + ":" + port}
		return u.String() + "?" + query.Encode() + "#" + frag, nil
	case "shadowsocks":
		if cfg.Password == "" || cfg.Method == "" {
			return "", errors.New("shadowsocks node requires config.method and config.password")
		}
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(cfg.Method + ":" + cfg.Password))
		return "ss://" + userinfo + "@" + host + ":" + port + "#" + frag, nil
	default:
		return "", fmt.Errorf("unsupported protocol %q", n.Protocol)
	}
}

// PackV2Ray 返回按协议链接逐行拼接后的 base64（v2ray 客户端订阅格式）。
// 空列表返回空串：无可用节点时订阅仍可刷新，客户端按 0 节点展示。
func PackV2Ray(nodes []storage.Node) (string, error) {
	links := make([]string, 0, len(nodes))
	for i := range nodes {
		link, err := ShareLink(&nodes[i])
		if err != nil {
			return "", fmt.Errorf("node %s: %w", nodes[i].Name, err)
		}
		links = append(links, link)
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n"))), nil
}
