package config

import (
	"testing"
	"time"
)

func TestEnvOr(t *testing.T) {
	t.Setenv("TEST_CFG_STR", "value")
	if got := envOr("TEST_CFG_STR", "fallback"); got != "value" {
		t.Errorf("envOr set = %q; want value", got)
	}
	if got := envOr("TEST_CFG_STR_UNSET", "fallback"); got != "fallback" {
		t.Errorf("envOr unset = %q; want fallback", got)
	}
}

func TestEnvIntOr(t *testing.T) {
	t.Setenv("TEST_CFG_INT", "42")
	if got := envIntOr("TEST_CFG_INT", 7); got != 42 {
		t.Errorf("envIntOr set = %d; want 42", got)
	}
	if got := envIntOr("TEST_CFG_INT_UNSET", 7); got != 7 {
		t.Errorf("envIntOr unset = %d; want 7", got)
	}
	t.Setenv("TEST_CFG_INT_BAD", "abc")
	if got := envIntOr("TEST_CFG_INT_BAD", 7); got != 7 {
		t.Errorf("envIntOr bad = %d; want 7", got)
	}
}

func TestEnvBoolOr(t *testing.T) {
	for _, s := range []string{"1", "true", "yes", "on", "TRUE", "Yes", "ON"} {
		t.Setenv("TEST_CFG_BOOL", s)
		if got := envBoolOr("TEST_CFG_BOOL", false); !got {
			t.Errorf("envBoolOr(%q) = false; want true", s)
		}
	}
	for _, s := range []string{"0", "false", "no", "off", "", "bogus"} {
		t.Setenv("TEST_CFG_BOOL", s)
		if got := envBoolOr("TEST_CFG_BOOL", false); got {
			t.Errorf("envBoolOr(%q) = true; want false", s)
		}
	}
	t.Setenv("TEST_CFG_BOOL", "bogus")
	if got := envBoolOr("TEST_CFG_BOOL", true); !got {
		t.Errorf("envBoolOr(bogus) fallback=true = false; want true")
	}
}

func TestLoadDefaults(t *testing.T) {
	c := Load()
	if c.Addr != ":8080" {
		t.Errorf("Addr = %q; want :8080", c.Addr)
	}
	if c.DBDriver != "sqlite" {
		t.Errorf("DBDriver = %q; want sqlite", c.DBDriver)
	}
	if c.HeartbeatIntervalSec != 30 {
		t.Errorf("HeartbeatIntervalSec = %d; want 30", c.HeartbeatIntervalSec)
	}
	if c.AllocIntervalSec != 300 {
		t.Errorf("AllocIntervalSec = %d; want 300", c.AllocIntervalSec)
	}
	if c.BackupKeep != 7 {
		t.Errorf("BackupKeep = %d; want 7", c.BackupKeep)
	}
	if c.StandbySec != 600 {
		t.Errorf("StandbySec = %d; want 600", c.StandbySec)
	}
	if c.EventFlushIntervalSec != 30 {
		t.Errorf("EventFlushIntervalSec = %d; want 30", c.EventFlushIntervalSec)
	}
	if c.RedeemLimiter == nil || c.RedeemLimiter.MaxAttempts != 10 || c.RedeemLimiter.Lockout != 15*time.Minute {
		t.Errorf("RedeemLimiter = %+v; want MaxAttempts 10 Lockout 15m", c.RedeemLimiter)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("FERRY_ADDR", ":9999")
	t.Setenv("FERRY_HEARTBEAT_SEC", "45")
	t.Setenv("FERRY_BACKUP_S3_ENABLED", "1")
	c := Load()
	if c.Addr != ":9999" {
		t.Errorf("Addr = %q; want :9999", c.Addr)
	}
	if c.HeartbeatIntervalSec != 45 {
		t.Errorf("HeartbeatIntervalSec = %d; want 45", c.HeartbeatIntervalSec)
	}
	if !c.BackupS3Enabled {
		t.Error("BackupS3Enabled = false; want true")
	}
}

func TestDefault(t *testing.T) {
	c := Default()
	if c.Addr != ":8080" || c.DBDriver != "sqlite" || c.DBDSN != "ferry.db" {
		t.Errorf("Default basics = %+v", c)
	}
	if c.AdminUser != "admin" {
		t.Errorf("AdminUser = %q; want admin", c.AdminUser)
	}
}
