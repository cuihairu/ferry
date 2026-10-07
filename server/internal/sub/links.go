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
	"time"

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
	return shareLink(n, n.Name)
}

// shareLink 与 ShareLink 同体，备注可指定（订阅侧带区域与延迟，E-19）。
func shareLink(n *storage.Node, remark string) (string, error) {
	cfg, err := parseConfig(n.Config)
	if err != nil {
		return "", err
	}
	host := n.Address
	port := strconv.Itoa(n.Port)
	frag := url.PathEscape(remark)
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
			"v": "2", "ps": remark, "add": host, "port": port, "id": cfg.UUID,
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

// Entry 是下发给客户端的一个入口：节点本体 + 面板聚合的测速延迟（E-19）。
// RttMs 为边缘探测结论的近期聚合值，0 表示暂无数据。
type Entry struct {
	Node  storage.Node
	RttMs int
}

// entryRegion 展示用区域名：空值回退「未知」。
func entryRegion(e *Entry) string {
	if e.Node.Region == "" {
		return "未知"
	}
	return e.Node.Region
}

// entryRemark v2ray 无真分组，靠排序 + 备注表达分组：
// 「区域 · 名称 (延迟ms)」，无测速数据省略括注。
func entryRemark(e *Entry) string {
	region := entryRegion(e)
	if e.RttMs > 0 {
		return fmt.Sprintf("%s · %s (%dms)", region, e.Node.Name, e.RttMs)
	}
	return fmt.Sprintf("%s · %s", region, e.Node.Name)
}

// entryLink 生成带备注的分享链接（协议内字段：URI 片段 / vmess ps）。
func entryLink(e *Entry) (string, error) {
	return shareLink(&e.Node, entryRemark(e))
}

// PackV2Ray 返回按区域分组排序的链接列表 base64（v2ray 客户端订阅格式，E-19）。
// notes 是备注文本（TOUCH-7 断联容灾）：逐条以 "# " 注释行插在解码文本头部，
// 客户端逐行解析时忽略非链接行——订阅缓存里自带逃生通道。空列表仍输出备注：
// 无可用节点时订阅可刷新，用户手里留着备用入口与公告地址。
func PackV2Ray(entries []Entry, notes []string) (string, error) {
	sorted := orderByProbe(entries, time.Now())
	lines := make([]string, 0, len(notes)+len(sorted))
	for _, n := range notes {
		lines = append(lines, "# "+n)
	}
	for i := range sorted {
		link, err := entryLink(&sorted[i])
		if err != nil {
			return "", fmt.Errorf("node %s: %w", sorted[i].Node.Name, err)
		}
		lines = append(lines, link)
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n"))), nil
}
