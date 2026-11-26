// Package config loads the agent runtime configuration: the file is JSON, and environment variables may override key items.
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

// TLSConfig declares the mTLS related files; when all of them are empty, only the node token is used and verification runs against the system root certificates.
type TLSConfig struct {
	CAFile   string `json:"ca_file"`   // CA used to verify the panel certificate (required for self-signed deployments)
	CertFile string `json:"cert_file"` // client certificate (when mTLS is enabled)
	KeyFile  string `json:"key_file"`
}

// ProcSpec describes one managed proxy process and is used by the process management feature.
type ProcSpec struct {
	Name       string   `json:"name"`        // process name; panel messages are addressed by it
	Kind       string   `json:"kind"`        // xray / sing-box / hysteria2
	Exec       string   `json:"exec"`        // path to the executable
	Args       []string `json:"args"`        // startup arguments
	WorkDir    string   `json:"work_dir"`    // working directory
	ConfigPath string   `json:"config_path"` // config file path, also where a config push lands
	AssetDir   string   `json:"asset_dir"`   // rule library asset directory (where a rulelib push lands); empty falls back to assets/ alongside config_path
	StatsAPI   string   `json:"stats_api"`   // xray gRPC stats API address (e.g. 127.0.0.1:10085); empty = no traffic collection for this process
	MetricsURL string   `json:"metrics_url"` // HTTP GET metrics endpoint (text key-value lines / a JSON numeric object); empty = no collection
	Reload     string   `json:"reload"`      // reload policy: restart / signal
	Validate   string   `json:"validate"`    // validation command template with a {config} placeholder; empty skips validation
}

// ProbeSpec describes one edge probing task: exit baselines and inbound peer probing run locally,
// while tunnel probing goes through the relay data plane (skipped until E-5/E-6 are connected).
type ProbeSpec struct {
	Name        string `json:"name"`        // task name
	TargetKind  string `json:"target_kind"` // tunnel/exit/peer
	Target      string `json:"target"`      // target host:port
	Direction   string `json:"direction"`   // out/in, default out
	IntervalSec int    `json:"interval_sec"`
}

// RelaySpec holds the runtime parameters of the relay data plane (E-5): listen locally, then dial the landing through the transport plugin.
type RelaySpec struct {
	Listen      string `json:"listen"`       // local listener, e.g. 127.0.0.1:1080
	LandingAddr string `json:"landing_addr"` // landing address, e.g. landing.example.com:443
	Tunnel      string `json:"tunnel"`       // transport plugin name; empty = the default tls-camo
	ServerName  string `json:"server_name"`  // TLS camouflage domain; empty = the landing hostname; ssh = an sshd address
	CAFile      string `json:"ca_file"`      // private CA; empty = the system root certificates
	// MaxConns caps concurrent forwarded connections (F-0808): the limit can be
	// changed on hot reload; 0 falls back to defaultMaxConns.
	MaxConns int `json:"max_conns,omitempty"`
	// MaxStreams caps concurrent outbound backend dials (F-0809); 0 means unlimited
	// (the P0 relay contract is one stream per forwarded connection).
	MaxStreams int `json:"max_streams,omitempty"`
	// RateLimitKbps caps per-connection bandwidth in kilobits/sec, per direction (F-0810); 0 = unlimited.
	RateLimitKbps int `json:"rate_limit_kbps,omitempty"`
	// DialRetries caps the landing dial retries per forwarded connection (F-0813);
	// 0 means the default retry count.
	DialRetries int `json:"dial_retries,omitempty"`
	// IdleTimeoutSec closes a forwarded connection after this many seconds without
	// traffic in either direction (F-0814); 0 disables the idle cleanup.
	IdleTimeoutSec int `json:"idle_timeout_sec,omitempty"`
	// MetricsListen is an optional local HTTP endpoint (e.g. 127.0.0.1:10090) exposing
	// /metrics counters (F-0815); empty disables it.
	MetricsListen string `json:"metrics_listen,omitempty"`

	// ssh plugin parameters (E-28, ignored by other transports): the key and known_hosts are local
	// path references, and the key content goes neither into config nor into the database (the credential encryption scope R24 covers only in-database secrets).
	SSHUser        string `json:"ssh_user,omitempty"`         // SSH login user, publickey authentication
	SSHKeyFile     string `json:"ssh_key_file,omitempty"`     // private key file path
	SSHKnownHosts  string `json:"ssh_known_hosts,omitempty"`  // known_hosts path; empty = skip host key verification (bootstrap phase only)
	SSHForwardAddr string `json:"ssh_forward_addr,omitempty"` // direct-tcpip target (the relay listener on the landing host)
}

// Config is every runtime parameter of the agent.
type Config struct {
	PanelURL     string              `json:"panel_url"` // e.g. wss://panel.example.com/agent/ws
	AgentID      string              `json:"agent_id"`
	Token        string              `json:"token"`
	HeartbeatSec int                 `json:"heartbeat_sec"`
	TrafficSec   int                 `json:"traffic_sec"` // traffic collection and reporting period; 0 = disabled
	SpoolDir     string              `json:"spool_dir"`   // spool directory for role reports; empty = no forwarding (E-5/E-7)
	TLS          TLSConfig           `json:"tls"`
	Meta         agentproto.NodeMeta `json:"meta"` // initial node registration metadata, reported with hello at registration time
	Procs        []ProcSpec          `json:"procs"`
	Probes       []ProbeSpec         `json:"probes"`
	Relay        RelaySpec           `json:"relay"` // used by -role relay; ignored in core mode
}

// Default returns a configuration populated with default values.
func Default() Config {
	return Config{HeartbeatSec: 30, TrafficSec: 60}
}

// Load reads the config file and applies environment variable overrides: FERRY_PANEL_URL / FERRY_NODE_TOKEN / FERRY_AGENT_ID / FERRY_HEARTBEAT_SEC.
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
	if v := os.Getenv("FERRY_TRAFFIC_SEC"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("FERRY_TRAFFIC_SEC: %w", err)
		}
		cfg.TrafficSec = n
	}
	if v := os.Getenv("FERRY_SPOOL_DIR"); v != "" {
		cfg.SpoolDir = v
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
	for i := range cfg.Probes {
		if cfg.Probes[i].Direction == "" {
			cfg.Probes[i].Direction = agentproto.DirectionOut
		}
	}
	applyMetaDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// applyMetaDefaults fills in missing metadata defaults, aligned with the nodes table defaults.
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

// Validate checks required items and value ranges.
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
	if c.TrafficSec != 0 && (c.TrafficSec < 10 || c.TrafficSec > 3600) {
		return errors.New("traffic_sec must be 0 (off) or 10-3600")
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
	probes := map[string]bool{}
	for _, p := range c.Probes {
		if p.Name == "" || p.Target == "" {
			return errors.New("probes[].name and probes[].target are required")
		}
		if probes[p.Name] {
			return fmt.Errorf("duplicate probe name %q", p.Name)
		}
		probes[p.Name] = true
		switch p.TargetKind {
		case agentproto.ProbeTargetTunnel, agentproto.ProbeTargetExit, agentproto.ProbeTargetPeer:
		default:
			return errors.New("probes[].target_kind must be tunnel/exit/peer")
		}
		if p.IntervalSec < 5 || p.IntervalSec > 3600 {
			return errors.New("probes[].interval_sec must be 5-3600")
		}
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

// HeartbeatInterval returns the heartbeat period.
func (c Config) HeartbeatInterval() time.Duration {
	return time.Duration(c.HeartbeatSec) * time.Second
}

// TrafficInterval returns the traffic collection period; 0 means disabled.
func (c Config) TrafficInterval() time.Duration {
	if c.TrafficSec <= 0 {
		return 0
	}
	return time.Duration(c.TrafficSec) * time.Second
}
