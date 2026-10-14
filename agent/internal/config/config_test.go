package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

func TestLoadDefaults(t *testing.T) {
	// 无文件且无环境变量时必填项缺失，加载应当报错。
	if _, err := Load(""); err == nil {
		t.Fatal("empty load must fail: panel_url/token required")
	}
	if err := Default().Validate(); err == nil {
		t.Fatal("default config must fail validation until url/token set")
	}
}

func TestLoadFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	content := `{
		"panel_url":"wss://p.example/agent/ws","agent_id":"n1","token":"t1","heartbeat_sec":15,
		"meta":{"role":"entry","direction":"out","line_type":"cn2_gia","region":"华东","city":"上海",
			"datacenter":"sh-1","isp":"电信","labels":["bgp"],"transport":"tls","billing_type":"按流量",
			"traffic_price_cents":120,"monthly_cost_cents":5000,"currency":"CNY",
			"bw_up_mbps":100,"bw_down_mbps":200,"rate_limited":true}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FERRY_NODE_TOKEN", "env-token")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.PanelURL != "wss://p.example/agent/ws" || cfg.AgentID != "n1" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	if cfg.Token != "env-token" {
		t.Fatalf("env must override file token, got %q", cfg.Token)
	}
	if cfg.HeartbeatSec != 15 {
		t.Fatalf("heartbeat = %d", cfg.HeartbeatSec)
	}
	if cfg.Meta.Role != agentproto.RoleEntry || cfg.Meta.ISP != "电信" ||
		cfg.Meta.Region != "华东" || cfg.Meta.BillingType != "按流量" {
		t.Fatalf("meta not loaded from file: %+v", cfg.Meta)
	}
	if cfg.Meta.BwUpMbps != 100 || cfg.Meta.BwDownMbps != 200 {
		t.Fatalf("bandwidth not loaded: up=%d down=%d", cfg.Meta.BwUpMbps, cfg.Meta.BwDownMbps)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestMetaDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	content := `{"panel_url":"wss://p/agent/ws","agent_id":"n1","token":"t1","heartbeat_sec":30,
		"meta":{"bw_up_mbps":100,"bw_down_mbps":100}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m := cfg.Meta
	if m.Role != agentproto.RoleLanding || m.Direction != agentproto.DirectionOut {
		t.Fatalf("role/direction defaults wrong: %+v", m)
	}
	if m.LineType != "普通" || m.Region != "未知" || m.ISP != "未知" {
		t.Fatalf("line/region/isp defaults wrong: %+v", m)
	}
	if m.Transport != "tls" || m.BillingType != "包月" || m.Currency != "CNY" {
		t.Fatalf("transport/billing/currency defaults wrong: %+v", m)
	}
	if m.ISP == "" || m.Region == "" {
		t.Fatal("region and isp must never be empty after defaults")
	}
}

func TestMetaEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	content := `{"panel_url":"wss://p/agent/ws","agent_id":"n1","token":"t1","heartbeat_sec":30,
		"meta":{"bw_up_mbps":100,"bw_down_mbps":100}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FERRY_NODE_ROLE", string(agentproto.RoleEntry))
	t.Setenv("FERRY_NODE_DIRECTION", string(agentproto.DirectionIn))
	t.Setenv("FERRY_NODE_REGION", "华北")
	t.Setenv("FERRY_NODE_ISP", "联通")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Meta.Role != agentproto.RoleEntry || cfg.Meta.Direction != agentproto.DirectionIn {
		t.Fatalf("role/direction env override failed: %+v", cfg.Meta)
	}
	if cfg.Meta.Region != "华北" || cfg.Meta.ISP != "联通" {
		t.Fatalf("region/isp env override failed: %+v", cfg.Meta)
	}
}

func TestProbeSpecValidation(t *testing.T) {
	base := func() Config {
		return Config{
			PanelURL: "wss://p/ws", Token: "t", HeartbeatSec: 30,
			Meta: agentproto.NodeMeta{
				Role: agentproto.RoleLanding, Direction: agentproto.DirectionOut,
				LineType: "普通", Region: "未知", ISP: "未知", Transport: "tls",
				BillingType: "包月", BwUpMbps: 100, BwDownMbps: 100,
			},
		}
	}
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"probe missing target", func(c *Config) {
			c.Probes = []ProbeSpec{{Name: "p"}}
		}},
		{"probe dup name", func(c *Config) {
			c.Probes = []ProbeSpec{
				{Name: "p", Target: "a:1"}, {Name: "p", Target: "b:2"},
			}
		}},
		{"probe bad kind", func(c *Config) {
			c.Probes = []ProbeSpec{{Name: "p", TargetKind: "udp", Target: "a:1"}}
		}},
		{"probe interval low", func(c *Config) {
			c.Probes = []ProbeSpec{{Name: "p", Target: "a:1", IntervalSec: 1}}
		}},
		{"probe interval high", func(c *Config) {
			c.Probes = []ProbeSpec{{Name: "p", Target: "a:1", IntervalSec: 3601}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mut(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestProbeDirectionDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	content := `{"panel_url":"wss://p/agent/ws","agent_id":"n1","token":"t1",
		"heartbeat_sec":30,"meta":{"bw_up_mbps":100,"bw_down_mbps":100},
		"probes":[{"name":"exit","target_kind":"exit","target":"www.example.com:443","interval_sec":30}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Probes) != 1 || cfg.Probes[0].Direction != agentproto.DirectionOut {
		t.Fatalf("probe direction default: %+v", cfg.Probes)
	}
}

func TestSpoolDirEnvAndRelay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	content := `{"panel_url":"wss://p/agent/ws","agent_id":"n1","token":"t1","heartbeat_sec":30,
		"meta":{"bw_up_mbps":100,"bw_down_mbps":100},
		"relay":{"listen":"127.0.0.1:1080","landing_addr":"l.example.com:443","tunnel":"tls-camo"}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FERRY_SPOOL_DIR", "/run/ferry/spool")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.SpoolDir != "/run/ferry/spool" {
		t.Fatalf("spool_dir env override failed: %q", cfg.SpoolDir)
	}
	if cfg.Relay.Listen != "127.0.0.1:1080" || cfg.Relay.LandingAddr != "l.example.com:443" ||
		cfg.Relay.Tunnel != "tls-camo" {
		t.Fatalf("relay spec not loaded: %+v", cfg.Relay)
	}
}

func TestValidate(t *testing.T) {
	base := func() Config {
		return Config{
			PanelURL: "wss://p/ws", Token: "t", HeartbeatSec: 30,
			Meta: agentproto.NodeMeta{
				Role: agentproto.RoleLanding, Direction: agentproto.DirectionOut,
				LineType: "普通", Region: "未知", ISP: "未知", Transport: "tls",
				BillingType: "包月", BwUpMbps: 100, BwDownMbps: 100,
			},
		}
	}
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"missing url", func(c *Config) { c.PanelURL = "" }},
		{"missing token", func(c *Config) { c.Token = "" }},
		{"heartbeat low", func(c *Config) { c.HeartbeatSec = 1 }},
		{"heartbeat high", func(c *Config) { c.HeartbeatSec = 601 }},
		{"cert without key", func(c *Config) { c.TLS.CertFile = "a.pem" }},
		{"dup proc", func(c *Config) {
			c.Procs = []ProcSpec{{Name: "x", Exec: "x"}, {Name: "x", Exec: "y"}}
		}},
		{"proc missing exec", func(c *Config) {
			c.Procs = []ProcSpec{{Name: "x"}}
		}},
		{"bad role", func(c *Config) { c.Meta.Role = "gateway" }},
		{"bad direction", func(c *Config) { c.Meta.Direction = "sideways" }},
		{"bad line_type", func(c *Config) { c.Meta.LineType = " premium" }},
		{"bad transport", func(c *Config) { c.Meta.Transport = "raw" }},
		{"bad billing_type", func(c *Config) { c.Meta.BillingType = "免费" }},
		{"empty region", func(c *Config) { c.Meta.Region = "" }},
		{"empty isp", func(c *Config) { c.Meta.ISP = "" }},
		{"missing bandwidth", func(c *Config) { c.Meta.BwUpMbps = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mut(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

// TestLoadErrors 读取/解析/env 数值三类失败都要冒错（不回退默认值糊过去）。
func TestLoadErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.json")
	if _, err := Load(missing); err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("missing file err = %v; want read config failure", err)
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("bad json err = %v; want parse config failure", err)
	}

	t.Setenv("FERRY_HEARTBEAT_SEC", "abc")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "FERRY_HEARTBEAT_SEC") {
		t.Fatalf("bad heartbeat env err = %v", err)
	}
	t.Setenv("FERRY_HEARTBEAT_SEC", "")
	t.Setenv("FERRY_TRAFFIC_SEC", "abc")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "FERRY_TRAFFIC_SEC") {
		t.Fatalf("bad traffic env err = %v", err)
	}
}

// TestLoadFileMetaEnvOverride 文件给必填项、env 逐项覆盖元数据。
func TestLoadFileMetaEnvOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	body := `{"panel_url":"https://panel.example.com","token":"tok-file","agent_id":"agent-file",
	  "meta":{"bw_up_mbps":10,"bw_down_mbps":50}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FERRY_PANEL_URL", "https://env.example.com")
	t.Setenv("FERRY_NODE_TOKEN", "tok-env")
	t.Setenv("FERRY_AGENT_ID", "agent-env")
	t.Setenv("FERRY_SPOOL_DIR", "/var/spool/ferry")
	t.Setenv("FERRY_NODE_ROLE", agentproto.RoleEntry)
	t.Setenv("FERRY_NODE_DIRECTION", agentproto.DirectionIn)
	t.Setenv("FERRY_NODE_REGION", "HK")
	t.Setenv("FERRY_NODE_ISP", "hkt")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load = %v", err)
	}
	if cfg.PanelURL != "https://env.example.com" || cfg.Token != "tok-env" || cfg.AgentID != "agent-env" {
		t.Fatalf("env should win over file: %+v", cfg)
	}
	if cfg.SpoolDir != "/var/spool/ferry" || cfg.Meta.Role != agentproto.RoleEntry ||
		cfg.Meta.Direction != agentproto.DirectionIn || cfg.Meta.Region != "HK" || cfg.Meta.ISP != "hkt" {
		t.Fatalf("meta env overrides not applied: %+v", cfg.Meta)
	}
	if cfg.Meta.BwUpMbps != 10 || cfg.Meta.BwDownMbps != 50 {
		t.Fatalf("file-provided bandwidth lost: %+v", cfg.Meta)
	}
}

// TestIntervals 周期口径：流量周期 <=0 表示关闭采集。
func TestIntervals(t *testing.T) {
	if got := (Config{HeartbeatSec: 30}).HeartbeatInterval(); got != 30*time.Second {
		t.Errorf("HeartbeatInterval = %v; want 30s", got)
	}
	if got := (Config{TrafficSec: 60}).TrafficInterval(); got != 60*time.Second {
		t.Errorf("TrafficInterval = %v; want 60s", got)
	}
	for _, sec := range []int{0, -1} {
		if got := (Config{TrafficSec: sec}).TrafficInterval(); got != 0 {
			t.Errorf("TrafficInterval(%d) = %v; want 0 (disabled)", sec, got)
		}
	}
}
