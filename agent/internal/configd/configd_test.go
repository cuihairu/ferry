package configd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
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

// TestRollbackRestoresOldContent 回滚口径：有旧文件时内容还原后重试 reload；
// reload 仍失败则冒错（不回滚成功）。
func TestRollbackRestoresOldContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	if err := os.WriteFile(target, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Deployer{}

	// 旧内容还原 + reload 成功
	loads := 0
	if err := d.rollback(target, []byte("old"), true, func() error { loads++; return nil }, ".tmp-*"); err != nil {
		t.Fatalf("rollback = %v", err)
	}
	if loads != 1 {
		t.Fatalf("reload calls = %d; want 1", loads)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "old" {
		t.Fatalf("content = %q, %v; want old", got, err)
	}
	// 临时文件不留残留
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries; want only target", len(entries))
	}

	// reload 仍失败：冒错不改内容
	wantErr := errors.New("reload boom")
	if err := d.rollback(target, []byte("older"), true, func() error { return wantErr }, ".tmp-*"); !errors.Is(err, wantErr) {
		t.Fatalf("rollback err = %v; want %v", err, wantErr)
	}
	if got, _ := os.ReadFile(target); string(got) != "older" {
		t.Fatalf("content = %q; want older", got)
	}
}

// TestRollbackWithoutOldFile 无旧文件时删掉新文件；文件已不在（IsNotExist）
// 视为回滚到位，reload 仍照跑。
func TestRollbackWithoutOldFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	if err := os.WriteFile(target, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Deployer{}
	loads := 0
	if err := d.rollback(target, nil, false, func() error { loads++; return nil }, ".tmp-*"); err != nil {
		t.Fatalf("rollback = %v", err)
	}
	if loads != 1 || !os.IsNotExist(mustStatErr(t, target)) {
		t.Fatalf("loads=%d, file removed=%v; want 1, true", loads, !os.IsNotExist(mustStatErr(t, target)))
	}

	// 文件已不存在：容忍 IsNotExist
	if err := d.rollback(target, nil, false, func() error { return nil }, ".tmp-*"); err != nil {
		t.Fatalf("rollback on absent file = %v", err)
	}

	// 目录不可写无法稳定构造，跳过；reload 失败口径在此覆盖：
	wantErr := errors.New("reload boom")
	if err := d.rollback(filepath.Join(dir, "absent.json"), nil, false, func() error { return wantErr }, ".tmp-*"); !errors.Is(err, wantErr) {
		t.Fatalf("rollback err = %v; want %v", err, wantErr)
	}
}

func mustStatErr(t *testing.T, path string) error {
	t.Helper()
	_, err := os.Stat(path)
	return err
}

func TestNewNilLogger(t *testing.T) {
	if d := New(nil); d == nil || d.log == nil {
		t.Fatal("New(nil) should fall back to log.Default()")
	}
}

// TestReadIfExists 目录读法报错；不存在按「无旧内容」口径返回。
func TestReadIfExists(t *testing.T) {
	dir := t.TempDir()
	raw, had, err := readIfExists(dir)
	if err == nil || had || raw != nil {
		t.Fatalf("readIfExists(dir) = %q, %v, %v; want error", raw, had, err)
	}
	raw, had, err = readIfExists(filepath.Join(dir, "absent.json"))
	if err != nil || had || raw != nil {
		t.Fatalf("readIfExists(absent) = %q, %v, %v; want nil,false,nil", raw, had, err)
	}
}

// TestApplyReadCurrentConfigError 配置路径指向目录：读旧内容即失败，不落盘。
func TestApplyReadCurrentConfigError(t *testing.T) {
	spec := testSpec(t, "")
	if err := os.MkdirAll(spec.ConfigPath, 0o755); err != nil {
		t.Fatal(err)
	}
	ack := New(nil).Apply(spec, pushFor("new"), func() error { return nil })
	if ack.OK || !strings.Contains(ack.Error, "read current config") {
		t.Fatalf("ack = %+v; want read current config failure", ack)
	}
}

// TestApplyRuleLibNoPaths 规则库既无 asset_dir 也无 config_path：不落盘。
func TestApplyRuleLibNoPaths(t *testing.T) {
	spec := config.ProcSpec{Name: "xray", Kind: "xray"}
	ack := New(nil).Apply(spec, rulelibPush("geoip.dat", "data"), func() error { return nil })
	if ack.OK || !strings.Contains(ack.Error, "neither config_path nor asset_dir") {
		t.Fatalf("ack = %+v; want no-path failure", ack)
	}
}

// TestApplyRuleLibMkdirFails asset_dir 指向普通文件：建目录失败即中止。
func TestApplyRuleLibMkdirFails(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := testSpec(t, "")
	spec.AssetDir = blocker
	ack := New(nil).Apply(spec, rulelibPush("geoip.dat", "data"), func() error { return nil })
	if ack.OK || !strings.Contains(ack.Error, "prepare asset dir") {
		t.Fatalf("ack = %+v; want prepare asset dir failure", ack)
	}
}

// TestApplyRuleLibRollbackAlsoFails 回滚后 reload 仍失败：两个错误都冒出来。
func TestApplyRuleLibRollbackAlsoFails(t *testing.T) {
	spec := testSpec(t, "")
	target := filepath.Join(filepath.Dir(spec.ConfigPath), "assets", "geoip.dat")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old-geo"), 0o644); err != nil {
		t.Fatal(err)
	}
	ack := New(nil).Apply(spec, rulelibPush("geoip.dat", "new-geo"),
		func() error { return errors.New("reload boom") })
	if ack.OK || !ack.Reverted {
		t.Fatalf("ack = %+v; want reverted failure", ack)
	}
	if !strings.Contains(ack.Error, "; rollback:") {
		t.Fatalf("error = %q; want both reload and rollback errors", ack.Error)
	}
	if got := readFile(t, target); got != "old-geo" {
		t.Fatalf("rulelib = %q; want old-geo restored anyway", got)
	}
}

// TestValidRuleLibNameEdge 补长度上限与前导 '-' 两处未覆盖分支。
func TestValidRuleLibNameEdge(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"-geoip.dat", false}, // 前导 '-' 拒绝
		{"geoip.dat", true},   // 正常名
		{"a..b", false},       // 含 .. 拒绝
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false}, // 超 64 拒绝
	}
	for _, c := range cases {
		if got := validRuleLibName(c.name); got != c.want {
			t.Errorf("validRuleLibName(%d chars) = %v; want %v", len(c.name), got, c.want)
		}
	}
}

// TestApplyRuleLibReadCurrentError asset 目标被同名目录占位：读现状即失败，
// 不落盘不 reload。
func TestApplyRuleLibReadCurrentError(t *testing.T) {
	spec := testSpec(t, "")
	dir := filepath.Join(filepath.Dir(spec.ConfigPath), "assets")
	if err := os.MkdirAll(filepath.Join(dir, "geoip.dat"), 0o755); err != nil {
		t.Fatal(err)
	}
	ack := New(nil).Apply(spec, rulelibPush("geoip.dat", "data"), func() error { return nil })
	if ack.OK || !strings.Contains(ack.Error, "read current rulelib") {
		t.Fatalf("ack = %+v; want read current rulelib failure", ack)
	}
}

// TestRollbackTempCreateFails 回滚临时文件落在文件占位目录：冒
// 「write rollback temp」而非静默丢旧内容。
func TestRollbackTempCreateFails(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Deployer{}
	err := d.rollback(filepath.Join(blocker, "cfg.json"), []byte("old"), true, func() error { return nil }, ".tmp-*")
	if err == nil || !strings.Contains(err.Error(), "write rollback temp") {
		t.Fatalf("rollback err = %v; want write rollback temp failure", err)
	}
}
