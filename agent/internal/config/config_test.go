package config

import (
	"os"
	"path/filepath"
	"testing"
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
	content := `{"panel_url":"wss://p.example/agent/ws","agent_id":"n1","token":"t1","heartbeat_sec":15}`
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
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidate(t *testing.T) {
	base := func() Config {
		return Config{PanelURL: "wss://p/ws", Token: "t", HeartbeatSec: 30}
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
