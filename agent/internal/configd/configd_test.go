package configd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

func pushFor(payload string) agentproto.ConfigPush {
	sum := sha256.Sum256([]byte(payload))
	return agentproto.ConfigPush{
		Proc:    "xray",
		Kind:    "xray",
		Version: "v1",
		Sha256:  hex.EncodeToString(sum[:]),
		Payload: payload,
	}
}

func testSpec(t *testing.T, validate string) config.ProcSpec {
	t.Helper()
	return config.ProcSpec{
		Name:       "xray",
		Kind:       "xray",
		ConfigPath: filepath.Join(t.TempDir(), "xray.json"),
		Validate:   validate,
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// reloadOK/reloadFail 是注入的 reload 桩，记录调用次数。
func reloadOK(n *int) func() error {
	return func() error {
		*n++
		return nil
	}
}

func reloadFail(n *int) func() error {
	return func() error {
		*n++
		return errors.New("reload boom")
	}
}

func TestApplyHappyPath(t *testing.T) {
	spec := testSpec(t, "test -f {config}")
	d := New(log.New(io.Discard, "", 0))
	if err := os.WriteFile(spec.ConfigPath, []byte(`{"old":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	ack := d.Apply(spec, pushFor(`{"new":true}`), reloadOK(&calls))
	if !ack.OK || !ack.Validated {
		t.Fatalf("want ok+validated, got %+v", ack)
	}
	if calls != 1 {
		t.Fatalf("reload calls = %d, want 1", calls)
	}
	if got := readFile(t, spec.ConfigPath); got != `{"new":true}` {
		t.Fatalf("config = %q, want new payload", got)
	}
}

func TestApplyShaMismatch(t *testing.T) {
	spec := testSpec(t, "")
	d := New(log.New(io.Discard, "", 0))
	if err := os.WriteFile(spec.ConfigPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	push := pushFor("new")
	push.Sha256 = "deadbeef"
	calls := 0
	ack := d.Apply(spec, push, reloadOK(&calls))
	if ack.OK || ack.Error == "" {
		t.Fatalf("want sha mismatch failure, got %+v", ack)
	}
	if calls != 0 {
		t.Fatalf("reload must not run, calls = %d", calls)
	}
	if got := readFile(t, spec.ConfigPath); got != "old" {
		t.Fatalf("config = %q, want untouched old", got)
	}
}

func TestApplyValidateFailsKeepsOld(t *testing.T) {
	spec := testSpec(t, "false")
	d := New(log.New(io.Discard, "", 0))
	if err := os.WriteFile(spec.ConfigPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	ack := d.Apply(spec, pushFor("new"), reloadOK(&calls))
	if ack.OK || ack.Validated {
		t.Fatalf("want validate failure, got %+v", ack)
	}
	if calls != 0 {
		t.Fatalf("reload must not run, calls = %d", calls)
	}
	if got := readFile(t, spec.ConfigPath); got != "old" {
		t.Fatalf("config = %q, want untouched old", got)
	}
	// 临时文件必须被清理。
	entries, err := os.ReadDir(filepath.Dir(spec.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(spec.ConfigPath) {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestApplyReloadFailRollsBack(t *testing.T) {
	spec := testSpec(t, "test -f {config}")
	d := New(log.New(io.Discard, "", 0))
	if err := os.WriteFile(spec.ConfigPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	ack := d.Apply(spec, pushFor("new"), reloadFail(&calls))
	if ack.OK || !ack.Reverted {
		t.Fatalf("want reverted failure, got %+v", ack)
	}
	if calls != 2 {
		t.Fatalf("reload calls = %d, want 2 (fail + rollback)", calls)
	}
	if got := readFile(t, spec.ConfigPath); got != "old" {
		t.Fatalf("config = %q, want rolled back to old", got)
	}
}

func TestApplyNoOldConfigRollbackRemoves(t *testing.T) {
	spec := testSpec(t, "")
	d := New(log.New(io.Discard, "", 0))

	calls := 0
	ack := d.Apply(spec, pushFor("new"), reloadFail(&calls))
	if ack.OK || !ack.Reverted {
		t.Fatalf("want reverted failure, got %+v", ack)
	}
	if _, err := os.Stat(spec.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("config should be removed when there was no old config, stat err = %v", err)
	}
}

func TestApplyEmptyConfigPath(t *testing.T) {
	spec := testSpec(t, "")
	spec.ConfigPath = ""
	d := New(log.New(io.Discard, "", 0))

	ack := d.Apply(spec, pushFor("new"), func() error { return nil })
	if ack.OK || ack.Error == "" {
		t.Fatalf("want config_path failure, got %+v", ack)
	}
}

func rulelibPush(name, payload string) agentproto.ConfigPush {
	return agentproto.ConfigPush{
		Proc:    "xray",
		Kind:    "rulelib:" + name,
		Version: wantSha(payload),
		Sha256:  wantSha(payload),
		Payload: payload,
	}
}

func wantSha(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func TestApplyRuleLibFallbackAssetDir(t *testing.T) {
	spec := testSpec(t, "")
	d := New(log.New(io.Discard, "", 0))

	calls := 0
	ack := d.Apply(spec, rulelibPush("geoip.dat", "geo-data-v1"), reloadOK(&calls))
	if !ack.OK || ack.Reverted {
		t.Fatalf("want ok, got %+v", ack)
	}
	if calls != 1 {
		t.Fatalf("reload calls = %d, want 1", calls)
	}
	// 未配置 AssetDir：回退主配置同级 assets/
	want := filepath.Join(filepath.Dir(spec.ConfigPath), "assets", "geoip.dat")
	if got := readFile(t, want); got != "geo-data-v1" {
		t.Fatalf("rulelib = %q, want payload", got)
	}
}

func TestApplyRuleLibExplicitAssetDir(t *testing.T) {
	spec := testSpec(t, "")
	spec.AssetDir = filepath.Join(t.TempDir(), "xray-assets")
	d := New(log.New(io.Discard, "", 0))

	ack := d.Apply(spec, rulelibPush("geosite.dat", "geosite-data"), func() error { return nil })
	if !ack.OK {
		t.Fatalf("want ok, got %+v", ack)
	}
	if got := readFile(t, filepath.Join(spec.AssetDir, "geosite.dat")); got != "geosite-data" {
		t.Fatalf("rulelib = %q, want payload", got)
	}
}

func TestApplyRuleLibRollback(t *testing.T) {
	spec := testSpec(t, "")
	d := New(log.New(io.Discard, "", 0))
	target := filepath.Join(filepath.Dir(spec.ConfigPath), "assets", "geoip.dat")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old-geo"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	ack := d.Apply(spec, rulelibPush("geoip.dat", "new-geo"), reloadFail(&calls))
	if ack.OK || !ack.Reverted {
		t.Fatalf("want reverted failure, got %+v", ack)
	}
	if got := readFile(t, target); got != "old-geo" {
		t.Fatalf("rulelib = %q, want rolled back to old", got)
	}
}

func TestApplyRuleLibBadNames(t *testing.T) {
	spec := testSpec(t, "")
	d := New(log.New(io.Discard, "", 0))

	for _, name := range []string{"", "..", "a/b", `a\b`, "..geoip.dat", ".geoip.dat"} {
		ack := d.Apply(spec, rulelibPush(name, "x"), func() error { return nil })
		if ack.OK || ack.Error == "" {
			t.Fatalf("name %q: want rejection, got %+v", name, ack)
		}
	}
	// 非法名绝不落盘
	if _, err := os.Stat(filepath.Join(filepath.Dir(spec.ConfigPath), "assets")); !os.IsNotExist(err) {
		t.Fatalf("rejected name should not touch disk, stat err = %v", err)
	}
}

func TestApplyRuleLibShaMismatch(t *testing.T) {
	spec := testSpec(t, "")
	d := New(log.New(io.Discard, "", 0))

	push := rulelibPush("geoip.dat", "data")
	push.Sha256 = "deadbeef"
	ack := d.Apply(spec, push, func() error { return nil })
	if ack.OK || ack.Error == "" {
		t.Fatalf("want sha failure, got %+v", ack)
	}
}
