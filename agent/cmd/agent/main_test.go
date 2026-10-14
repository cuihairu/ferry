package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cuihairu/ferry/agent/internal/config"
)

func TestEnvOr(t *testing.T) {
	const key = "FERRY_AGENT_CONFIG_TEST"
	if got := envOr(key, "fallback.json"); got != "fallback.json" {
		t.Errorf("envOr(unset) = %q; want fallback.json", got)
	}
	t.Setenv(key, "/etc/ferry/agent.json")
	if got := envOr(key, "fallback.json"); got != "/etc/ferry/agent.json" {
		t.Errorf("envOr(set) = %q; want env value", got)
	}
	// 空值视作未设（envOr 口径：只有非空才覆盖）。
	t.Setenv(key, "")
	if got := envOr(key, "fallback.json"); got != "fallback.json" {
		t.Errorf("envOr(empty) = %q; want fallback.json", got)
	}
}

// TestRunRoleDispatch 角色分发口径：未知角色拒、probe 对已取消 ctx 立即
// 回收、relay 无配置/坏覆盖文件都冒配置错不冒称成功。
func TestRunRoleDispatch(t *testing.T) {
	if err := runRole(context.Background(), "bogus", config.Config{}, ""); err == nil {
		t.Fatal("unknown role must fail")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runRole(ctx, "probe", config.Config{}, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("probe on cancelled ctx = %v; want canceled", err)
	}
	if err := runRole(ctx, "relay", config.Config{}, ""); err == nil {
		t.Fatal("relay without spec must fail (listen required)")
	}

	// relay 覆盖文件存在但损坏：loadOverride 解析失败即启停，不静默落回。
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := config.Config{Relay: config.RelaySpec{Listen: "127.0.0.1:0", LandingAddr: "127.0.0.1:1"}}
	if err := runRole(ctx, "relay", spec, bad); err == nil {
		t.Fatal("relay with corrupt override must fail")
	}
}
