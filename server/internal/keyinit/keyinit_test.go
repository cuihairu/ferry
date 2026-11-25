package keyinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
)

// testCfg builds a baseline config with an isolated temp directory (sqlite DB and the key file both land in it).
func testCfg(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Env = ""
	cfg.KeyFile = ""
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = filepath.Join(dir, "test.db")
	return cfg
}

// TestGeneratePersistsAndStable: first start generates and persists the three keys (0600), and a restart gets the same keys.
func TestGeneratePersistsAndStable(t *testing.T) {
	cfg := testCfg(t)
	got, res, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	for name, v := range map[string]string{
		"admin": got.AdminSecret, "api": got.ApiTokenSecret, "master": got.SecretKey,
	} {
		if len(v) < 32 {
			t.Fatalf("%s key too short after generate: %d bytes", name, len(v))
		}
	}
	if len(res.Generated) != 3 {
		t.Fatalf("generated = %v want all three", res.Generated)
	}
	if res.KeyFile == "" {
		t.Fatal("generated keys must be persisted to a keyfile")
	}
	info, err := os.Stat(res.KeyFile)
	if err != nil {
		t.Fatalf("stat keyfile: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("keyfile perm = %o want 600", perm)
	}

	// Restart stability: resolving the same path again must return the same key set (never regenerated per run).
	cfg2 := testCfg(t)
	cfg2.KeyFile = res.KeyFile
	got2, res2, err := Resolve(cfg2)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if len(res2.Generated) != 0 {
		t.Fatalf("second resolve regenerated %v, want stable reload", res2.Generated)
	}
	if got2.AdminSecret != got.AdminSecret || got2.ApiTokenSecret != got.ApiTokenSecret || got2.SecretKey != got.SecretKey {
		t.Fatal("keys changed across restarts")
	}
}

// TestEnvPrecedence: an explicitly configured env value wins over the key file, and env values are never persisted.
func TestEnvPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfg := testCfg(t)
	cfg.KeyFile = filepath.Join(dir, "keys.env")
	// Explicit config arrives via cfg (config.Load already read the env into cfg; Resolve does not re-read the env).
	cfg.AdminSecret = "env-admin-secret-0123456789"
	cfg.ApiTokenSecret = "env-api-secret-0123456789abcd"

	got, res, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.AdminSecret != "env-admin-secret-0123456789" || got.ApiTokenSecret != "env-api-secret-0123456789abcd" {
		t.Fatalf("env keys not honored: admin=%q api=%q", got.AdminSecret, got.ApiTokenSecret)
	}
	// master has no env value -> generated and persisted; the two env keys must not be written into the file.
	raw, err := os.ReadFile(res.KeyFile)
	if err != nil {
		t.Fatalf("read keyfile: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, "env-admin-secret") || strings.Contains(body, "env-api-secret") {
		t.Fatal("env-provided keys must not be persisted to the keyfile")
	}
	if !strings.Contains(body, "FERRY_SECRET_KEY=") {
		t.Fatal("generated master key missing from keyfile")
	}
}

// TestKeyfileRoundTrip: values in an existing key file are loaded and used, not regenerated.
func TestKeyfileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	kf := filepath.Join(dir, "keys.env")
	content := "# 注释行\nFERRY_ADMIN_SECRET=file-admin-key-0123456789\nFERRY_API_SECRET=file-api-key-0123456789\nFERRY_SECRET_KEY=file-master-key-0123456789\n"
	if err := os.WriteFile(kf, []byte(content), 0o600); err != nil {
		t.Fatalf("seed keyfile: %v", err)
	}
	cfg := testCfg(t)
	cfg.KeyFile = kf
	got, res, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.AdminSecret != "file-admin-key-0123456789" || got.ApiTokenSecret != "file-api-key-0123456789" || got.SecretKey != "file-master-key-0123456789" {
		t.Fatalf("keyfile values not honored: %+v", got)
	}
	if len(res.Generated) != 0 {
		t.Fatalf("regenerated despite keyfile: %v", res.Generated)
	}
}

// TestProductionRejectsDevFallbackValues: production refuses to boot when the dev default constants are configured explicitly.
func TestProductionRejectsDevFallbackValues(t *testing.T) {
	dir := t.TempDir()
	cfg := testCfg(t)
	cfg.Env = "production"
	cfg.KeyFile = filepath.Join(dir, "keys.env")
	cfg.AdminSecret = "ferry-admin-secret"

	_, _, err := Resolve(cfg)
	if err == nil || !strings.Contains(err.Error(), "development default") {
		t.Fatalf("production dev-constant admin secret err = %v want refusal", err)
	}
}

// TestProductionRejectsShortKey: production refuses a short key (<16) to boot; dev only warns.
func TestProductionRejectsShortKey(t *testing.T) {
	dir := t.TempDir()
	cfg := testCfg(t)
	cfg.Env = "production"
	cfg.KeyFile = filepath.Join(dir, "keys.env")
	cfg.SecretKey = "short"

	if _, _, err := Resolve(cfg); err == nil || !strings.Contains(err.Error(), "FERRY_SECRET_KEY") {
		t.Fatalf("production short key err = %v want refusal", err)
	}

	cfg.Env = ""
	if got, res, err := Resolve(cfg); err != nil {
		t.Fatalf("dev short key should only warn: %v", err)
	} else if len(res.Warnings) == 0 {
		t.Fatal("dev short key should carry a warning")
	} else if got.SecretKey != "short" {
		t.Fatalf("dev short key must stay as configured, got %q", got.SecretKey)
	}
}

// TestProductionRequiresPersistence: production refuses to boot when the key file is not writable (nowhere to persist).
func TestProductionRequiresPersistence(t *testing.T) {
	dir := t.TempDir()
	cfg := testCfg(t)
	cfg.Env = "production"
	// Points at a path that does not exist and cannot be created (the directory is missing).
	cfg.KeyFile = filepath.Join(dir, "no-such-dir", "keys.env")

	if _, _, err := Resolve(cfg); err == nil {
		t.Fatal("production must refuse when keys cannot be established persistently")
	}
}

// TestDevDefaultsWhenUnpersistable: in dev, with no env and no key file, this does not block -
// it falls back to the legacy dev defaults (only to run locally) and reports a warning.
func TestDevDefaultsWhenUnpersistable(t *testing.T) {
	cfg := config.Default()
	cfg.Env = ""
	cfg.KeyFile = ""
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = ":memory:"
	got, res, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("dev resolve: %v", err)
	}
	if got.AdminSecret != "" || got.ApiTokenSecret != "" {
		t.Fatalf("dev unpersistable must fall back to config defaults, got admin=%q api=%q", got.AdminSecret, got.ApiTokenSecret)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("falling back without persistence must warn")
	}
}

// TestKeyfileMergeKeepsExisting: when the key file already holds some keys, only the missing ones are added, existing lines untouched.
func TestKeyfileMergeKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	kf := filepath.Join(dir, "keys.env")
	if err := os.WriteFile(kf, []byte("FERRY_ADMIN_SECRET=existing-admin-0123456789\n"), 0o600); err != nil {
		t.Fatalf("seed keyfile: %v", err)
	}
	cfg := testCfg(t)
	cfg.KeyFile = kf
	got, _, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.AdminSecret != "existing-admin-0123456789" {
		t.Fatalf("existing admin key overwritten: %q", got.AdminSecret)
	}
	raw, err := os.ReadFile(kf)
	if err != nil {
		t.Fatalf("read keyfile: %v", err)
	}
	body := string(raw)
	if !strings.HasPrefix(body, "FERRY_ADMIN_SECRET=existing-admin-0123456789\n") {
		t.Fatalf("existing keyfile line not preserved: %q", body)
	}
	for _, k := range []string{"FERRY_API_SECRET=", "FERRY_SECRET_KEY="} {
		if !strings.Contains(body, k) {
			t.Fatalf("missing key %s after merge:\n%s", k, body)
		}
	}
}
