// sing-box 出站 JSON 生成（hy2 批）：与 clash/v2ray 同一 Entry 源，
// 输出 sing-box outbounds 数组片段（订阅/客户端配置可直接并入）。
// 口径：只做数据整形（ferry 不渲染内核完整配置，opaque payload 下发），
// 每协议一个 outbound 对象；不支持的协议返回错误（与 ShareLink 同口径）。
package sub

import (
	"encoding/json"
	"fmt"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// OutboundOf 把节点编码为 sing-box outbound 对象。当前支持 hysteria2；
// 其余协议沿用各自的既有出口（v2ray 链接 / clash YAML），缺了再补。
func OutboundOf(n *storage.Node) (map[string]any, error) {
	cfg, err := parseConfig(n.Config)
	if err != nil {
		return nil, err
	}
	switch n.Protocol {
	case "hysteria2":
		if cfg.Password == "" {
			return nil, fmt.Errorf("hysteria2 requires config.password")
		}
		ob := map[string]any{
			"type":        "hysteria2",
			"tag":         n.Name,
			"server":      n.Address,
			"server_port": n.Port,
			"password":    cfg.Password,
		}
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
		return ob, nil
	default:
		return nil, fmt.Errorf("unsupported protocol %q for sing-box outbound", n.Protocol)
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
