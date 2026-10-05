// Package config 加载 agent 运行配置：文件为 JSON，环境变量可覆盖关键项。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TLSConfig 声明 mTLS 相关文件；全部为空时仅用节点令牌走系统根证书校验。
type TLSConfig struct {
	CAFile   string `json:"ca_file"`   // 校验面板证书的 CA（自签部署时必填）
	CertFile string `json:"cert_file"` // 客户端证书（mTLS 开启时）
	KeyFile  string `json:"key_file"`
}

// ProcSpec 描述一个被管代理进程，进程管理功能使用。
type ProcSpec struct {
	Name       string   `json:"name"`        // 进程名，面板消息按此寻址
	Kind       string   `json:"kind"`        // xray / sing-box / hysteria2
	Exec       string   `json:"exec"`        // 可执行文件路径
	Args       []string `json:"args"`        // 启动参数
	WorkDir    string   `json:"work_dir"`    // 工作目录
	ConfigPath string   `json:"config_path"` // 配置文件路径，配置下发落盘点
	Reload     string   `json:"reload"`      // reload 策略：restart / signal
	Validate   string   `json:"validate"`    // 校验命令模板，{config} 占位符；空则跳过校验
}

// Config 是 agent 的全部运行参数。
type Config struct {
	PanelURL     string              `json:"panel_url"` // 形如 wss://panel.example.com/agent/ws
	AgentID      string              `json:"agent_id"`
	Token        string              `json:"token"`
	HeartbeatSec int                 `json:"heartbeat_sec"`
	TLS          TLSConfig           `json:"tls"`
	Meta         agentproto.NodeMeta `json:"meta"` // 节点注册元数据初值，注册时随 hello 上报
	Procs        []ProcSpec          `json:"procs"`
}

// Default 返回带默认值的配置。
func Default() Config {
	return Config{HeartbeatSec: 30}
}

// Load 读取配置文件并用环境变量覆盖：FERRY_PANEL_URL / FERRY_NODE_TOKEN / FERRY_AGENT_ID / FERRY_HEARTBEAT_SEC。
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read config: %w", err)
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return cfg, fmt.Errorf("parse config: %w", err)
		}
	}
	if v := os.Getenv("FERRY_PANEL_URL"); v != "" {
		cfg.PanelURL = v
	}
	if v := os.Getenv("FERRY_NODE_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("FERRY_AGENT_ID"); v != "" {
		cfg.AgentID = v
	}
	if v := os.Getenv("FERRY_HEARTBEAT_SEC"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("FERRY_HEARTBEAT_SEC: %w", err)
		}
		cfg.HeartbeatSec = n
	}
	if v := os.Getenv("FERRY_NODE_ROLE"); v != "" {
		cfg.Meta.Role = v
	}
	if v := os.Getenv("FERRY_NODE_DIRECTION"); v != "" {
		cfg.Meta.Direction = v
	}
	if v := os.Getenv("FERRY_NODE_REGION"); v != "" {
		cfg.Meta.Region = v
	}
	if v := os.Getenv("FERRY_NODE_ISP"); v != "" {
		cfg.Meta.ISP = v
	}
	applyMetaDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// applyMetaDefaults 补齐元数据缺省值，与 nodes 表默认值对齐。
func applyMetaDefaults(cfg *Config) {
	m := &cfg.Meta
	if m.Role == "" {
		m.Role = agentproto.RoleLanding
	}
	if m.Direction == "" {
		m.Direction = agentproto.DirectionOut
	}
	if m.LineType == "" {
		m.LineType = "普通"
	}
	if m.Region == "" {
		m.Region = "未知"
	}
	if m.ISP == "" {
		m.ISP = "未知"
	}
	if m.Transport == "" {
		m.Transport = "tls"
	}
	if m.BillingType == "" {
		m.BillingType = "包月"
	}
	if m.Currency == "" {
		m.Currency = "CNY"
	}
}

// Validate 校验必填项与取值范围。
func (c Config) Validate() error {
	if c.PanelURL == "" {
		return errors.New("panel_url is required")
	}
	if c.Token == "" {
		return errors.New("token is required")
	}
	if c.HeartbeatSec < 5 || c.HeartbeatSec > 600 {
		return errors.New("heartbeat_sec must be 5-600")
	}
	if (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") {
		return errors.New("tls.cert_file and tls.key_file must be set together")
	}
	names := map[string]bool{}
	for _, p := range c.Procs {
		if p.Name == "" || p.Exec == "" {
			return errors.New("procs[].name and procs[].exec are required")
		}
		if names[p.Name] {
			return fmt.Errorf("duplicate proc name %q", p.Name)
		}
		names[p.Name] = true
	}
	switch c.Meta.Role {
	case agentproto.RoleEntry, agentproto.RoleLanding, agentproto.RoleBoth:
	default:
		return errors.New("meta.role must be entry/landing/both")
	}
	switch c.Meta.Direction {
	case agentproto.DirectionOut, agentproto.DirectionIn, agentproto.DirectionBoth:
	default:
		return errors.New("meta.direction must be out/in/both")
	}
	switch c.Meta.LineType {
	case "cn2_gia", "cu_vip", "cmi", "iplc", "163", "普通":
	default:
		return errors.New("meta.line_type must be cn2_gia/cu_vip/cmi/iplc/163/普通")
	}
	switch c.Meta.Transport {
	case "tls", "quic", "ws-tls", "ssh":
	default:
		return errors.New("meta.transport must be tls/quic/ws-tls/ssh")
	}
	switch c.Meta.BillingType {
	case "按流量", "包月", "固定带宽":
	default:
		return errors.New("meta.billing_type must be 按流量/包月/固定带宽")
	}
	if c.Meta.Region == "" {
		return errors.New("meta.region is required (use 未知 if unknown)")
	}
	if c.Meta.ISP == "" {
		return errors.New("meta.isp is required (use 未知 if unknown)")
	}
	if c.Meta.BwUpMbps <= 0 || c.Meta.BwDownMbps <= 0 {
		return errors.New("meta.bw_up_mbps and meta.bw_down_mbps are required")
	}
	return nil
}

// HeartbeatInterval 返回心跳周期。
func (c Config) HeartbeatInterval() time.Duration {
	return time.Duration(c.HeartbeatSec) * time.Second
}
