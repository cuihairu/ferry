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
	// An empty value counts as unset (envOr only overrides on non-empty).
	t.Setenv(key, "")
	if got := envOr(key, "fallback.json"); got != "fallback.json" {
		t.Errorf("envOr(empty) = %q; want fallback.json", got)
	}
}

// TestRunRoleDispatch covers role dispatch: unknown roles are rejected, probe on an already-cancelled
// ctx is reclaimed immediately, and relay without config or with a corrupt override fails on config.
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

	// A corrupt relay override file: a loadOverride parse failure aborts startup, no silent fallback.
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := config.Config{Relay: config.RelaySpec{Listen: "127.0.0.1:0", LandingAddr: "127.0.0.1:1"}}
	if err := runRole(ctx, "relay", spec, bad); err == nil {
		t.Fatal("relay with corrupt override must fail")
	}
}

// TestRunCoreCancelledCtx covers the core entry point: an already-cancelled ctx
// reclaims the connection loop immediately, without touching the network or hanging.
func TestRunCoreCancelledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runCore(ctx, config.Config{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("runCore = %v; want canceled", err)
	}
}
