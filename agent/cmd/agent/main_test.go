package main

import "testing"

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
