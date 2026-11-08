package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestLoadEnvHeartbeatAndTraffic 面板心跳/流量周期可经环境变量覆盖文件值。
func TestLoadEnvHeartbeatAndTraffic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	content := `{"panel_url":"wss://p.example/agent/ws","token":"t1","heartbeat_sec":15,
		"meta":{"bw_up_mbps":100,"bw_down_mbps":200}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FERRY_HEARTBEAT_SEC", "30")
	t.Setenv("FERRY_TRAFFIC_SEC", "60")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HeartbeatSec != 30 {
		t.Fatalf("heartbeat = %d; want env 30", cfg.HeartbeatSec)
	}
	if cfg.TrafficSec != 60 {
		t.Fatalf("traffic = %d; want env 60", cfg.TrafficSec)
	}
}

// validBase 组一份能过 Validate 的最小配置，再按用例破坏。
func validBase() Config {
	return Config{
		PanelURL:     "wss://p.example/agent/ws",
		Token:        "t1",
		HeartbeatSec: 30,
		Meta:         agentproto.NodeMeta{Role: agentproto.RoleLanding},
	}
}

// TestValidateTrafficSecRange traffic_sec 只允许 0（关）或 10-3600。
func TestValidateTrafficSecRange(t *testing.T) {
	cfg := validBase()
	cfg.TrafficSec = 5
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "traffic_sec") {
		t.Fatalf("err = %v; want traffic_sec range error", err)
	}
}

// TestValidateDuplicateProbeName 探测规格重名：拒绝。
func TestValidateDuplicateProbeName(t *testing.T) {
	cfg := validBase()
	cfg.Probes = []ProbeSpec{
		{Name: "same", Target: "127.0.0.1:443", TargetKind: agentproto.ProbeTargetTunnel, IntervalSec: 30},
		{Name: "same", Target: "127.0.0.1:8443", TargetKind: agentproto.ProbeTargetTunnel, IntervalSec: 30},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate probe name") {
		t.Fatalf("err = %v; want duplicate probe name error", err)
	}
}

// TestValidateProbeIntervalRange 探测周期出界：拒绝。
func TestValidateProbeIntervalRange(t *testing.T) {
	cfg := validBase()
	cfg.Probes = []ProbeSpec{
		{Name: "slow", Target: "127.0.0.1:443", TargetKind: agentproto.ProbeTargetTunnel, IntervalSec: 1},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "interval_sec") {
		t.Fatalf("err = %v; want interval_sec range error", err)
	}
}
