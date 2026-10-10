// sing-box 出站 JSON 生成（hy2 批建，SB 批补全五协议）：与 clash/v2ray 同一
// Entry 源，输出 sing-box outbounds 数组片段（订阅/客户端配置可直接并入）。
// 口径：只做数据整形（ferry 不渲染内核完整配置，opaque payload 下发），
// schema 取 sing-box 官方文档化 outbound 格式（纯 JSON，不 import 源码，
// 二进制适配不传染）；不支持的协议/传输返回错误（与 ShareLink 同口径）。
package sub

import (
	"encoding/json"
	"fmt"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// OutboundOf 把节点编码为 sing-box outbound 对象（五协议全覆盖）。
func OutboundOf(n *storage.Node) (map[string]any, error) {
	cfg, err := parseConfig(n.Config)
	if err != nil {
		return nil, err
	}
	ob := map[string]any{
		"type":        n.Protocol,
		"tag":         n.Name,
		"server":      n.Address,
		"server_port": n.Port,
	}
	switch n.Protocol {
	case "vless":
		if cfg.UUID == "" {
			return nil, fmt.Errorf("vless requires config.uuid")
		}
		ob["uuid"] = cfg.UUID
		if cfg.Flow != "" {
			ob["flow"] = cfg.Flow
		}
		if err := applySingBoxTLS(ob, cfg, cfg.TLS); err != nil {
			return nil, err
		}
	case "vmess":
		if cfg.UUID == "" {
			return nil, fmt.Errorf("vmess requires config.uuid")
		}
		ob["uuid"] = cfg.UUID
		ob["security"] = defaultString(cfg.Scy, "auto")
		ob["alter_id"] = cfg.AID
		if err := applySingBoxTLS(ob, cfg, cfg.TLS); err != nil {
			return nil, err
		}
	case "trojan":
		if cfg.Password == "" {
			return nil, fmt.Errorf("trojan requires config.password")
		}
		ob["password"] = cfg.Password
		// trojan 协议本体即 TLS（trojan:// 语义），tls.enabled 恒真。
		if err := applySingBoxTLS(ob, cfg, true); err != nil {
			return nil, err
		}
	case "shadowsocks":
		if cfg.Password == "" || cfg.Method == "" {
			return nil, fmt.Errorf("shadowsocks requires config.method and config.password")
		}
		ob["method"] = cfg.Method
		ob["password"] = cfg.Password
	case "hysteria2":
		if cfg.Password == "" {
			return nil, fmt.Errorf("hysteria2 requires config.password")
		}
		ob["password"] = cfg.Password
		if cfg.SNI != "" {
			ob["sni"] = cfg.SNI
		}
		if cfg.Insecure {
			ob["insecure"] = true
		}
		if cfg.Obfs != "" {
			ob["obfs"] = map[string]any{"type": cfg.Obfs}
			if cfg.ObfsPassword != "" {
				ob["obfs"].(map[string]any)["password"] = cfg.ObfsPassword
			}
		}
		if cfg.Up > 0 {
			ob["up_mbps"] = cfg.Up
		}
		if cfg.Down > 0 {
			ob["down_mbps"] = cfg.Down
		}
	default:
		return nil, fmt.Errorf("unsupported protocol %q for sing-box outbound", n.Protocol)
	}
	transport, err := singBoxTransport(cfg)
	if err != nil {
		return nil, err
	}
	if transport != nil {
		ob["transport"] = transport
	}
	return ob, nil
}

// applySingBoxTLS 写入 sing-box tls 对象（enabled 由调用方按协议口径给，
// vless/vmess 随 cfg.TLS，trojan 恒真）；未启用不出对象（sing-box 里
// server_name 只在 TLS 下有意义）；server_name 取 sni，insecure 透传。
func applySingBoxTLS(ob map[string]any, cfg nodeConfig, enabled bool) error {
	if !enabled {
		return nil
	}
	tls := map[string]any{"enabled": enabled}
	if cfg.SNI != "" {
		tls["server_name"] = cfg.SNI
	}
	if cfg.Insecure {
		tls["insecure"] = true
	}
	ob["tls"] = tls
	return nil
}

// singBoxTransport 按 net 写 sing-box transport 对象：与 clash 同构展开
// ws（path + Host 头）；grpc/http/httpupgrade 常用四型映射；未知传输
// 显式报错（sing-box 无 passthrough，错拼只会让客户端配置解析失败）。
func singBoxTransport(cfg nodeConfig) (map[string]any, error) {
	switch cfg.Net {
	case "", "tcp":
		return nil, nil
	case "ws":
		t := map[string]any{"type": "ws"}
		if cfg.Path != "" {
			t["path"] = cfg.Path
		}
		if cfg.Host != "" {
			t["headers"] = map[string]any{"Host": cfg.Host}
		}
		return t, nil
	case "grpc":
		t := map[string]any{"type": "grpc"}
		if cfg.Path != "" {
			t["service_name"] = cfg.Path
		}
		return t, nil
	case "http":
		t := map[string]any{"type": "http"}
		if cfg.Host != "" {
			t["host"] = []any{cfg.Host}
		}
		if cfg.Path != "" {
			t["path"] = cfg.Path
		}
		return t, nil
	case "httpupgrade":
		t := map[string]any{"type": "httpupgrade"}
		if cfg.Host != "" {
			t["host"] = cfg.Host
		}
		if cfg.Path != "" {
			t["path"] = cfg.Path
		}
		return t, nil
	default:
		return nil, fmt.Errorf("unsupported net %q for sing-box transport", cfg.Net)
	}
}

// PackSingBox 返回全部入口的 sing-box outbounds JSON 数组（缩进两格，
// 客户端并入 outbounds 段即可用）。
func PackSingBox(entries []Entry) (string, error) {
	out := make([]map[string]any, 0, len(entries))
	for i := range entries {
		ob, err := OutboundOf(&entries[i].Node)
		if err != nil {
			return "", fmt.Errorf("node %s: %w", entries[i].Node.Name, err)
		}
		out = append(out, ob)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
