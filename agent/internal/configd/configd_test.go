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

// reloadOK/reloadFail are injected reload stubs that record the call count.
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
	// the temp file must be cleaned up.
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
	// AssetDir unset: fall back to assets/ alongside the main config
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
	// illegal names are never written to disk
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

// TestRollbackRestoresOldContent covers rollback semantics: with an old file, restore its content and retry reload;
// if reload still fails, an error surfaces (no pretend success).
func TestRollbackRestoresOldContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	if err := os.WriteFile(target, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Deployer{}

	// old content restored and reload succeeds
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
	// no temp file residue left behind
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries; want only target", len(entries))
	}

	// reload still fails: an error surfaces and the content is unchanged
	wantErr := errors.New("reload boom")
	if err := d.rollback(target, []byte("older"), true, func() error { return wantErr }, ".tmp-*"); !errors.Is(err, wantErr) {
		t.Fatalf("rollback err = %v; want %v", err, wantErr)
	}
	if got, _ := os.ReadFile(target); string(got) != "older" {
		t.Fatalf("content = %q; want older", got)
	}
}

// TestRollbackWithoutOldFile covers no old file: the new file is removed; when the file is already gone (IsNotExist)
// it counts as the rollback being done, and reload still runs.
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

	// the file is already gone: tolerate IsNotExist
	if err := d.rollback(target, nil, false, func() error { return nil }, ".tmp-*"); err != nil {
		t.Fatalf("rollback on absent file = %v", err)
	}

	// an unwritable directory cannot be constructed reliably here, so it is skipped; reload failure semantics are covered here:
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

// TestReadIfExists covers a directory read error; a missing file is returned per the "no old content" rule.
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

// TestApplyReadCurrentConfigError covers the config path pointing at a directory: reading the old content fails, nothing is written.
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

// TestApplyRuleLibNoPaths covers the rule library with neither asset_dir nor config_path: nothing is written.
func TestApplyRuleLibNoPaths(t *testing.T) {
	spec := config.ProcSpec{Name: "xray", Kind: "xray"}
	ack := New(nil).Apply(spec, rulelibPush("geoip.dat", "data"), func() error { return nil })
	if ack.OK || !strings.Contains(ack.Error, "neither config_path nor asset_dir") {
		t.Fatalf("ack = %+v; want no-path failure", ack)
	}
}

// TestApplyRuleLibMkdirFails covers asset_dir pointing at a plain file: mkdir failing aborts the push.
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

// TestApplyRuleLibRollbackAlsoFails covers rollback still failing after reload failed: both errors surface.
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

// TestValidRuleLibNameEdge covers the length limit and the leading '-' branches that were untested.
func TestValidRuleLibNameEdge(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"-geoip.dat", false}, // leading '-' rejected
		{"geoip.dat", true},   // a normal name
		{"a..b", false},       // containing .. rejected
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false}, // over 64 rejected
	}
	for _, c := range cases {
		if got := validRuleLibName(c.name); got != c.want {
			t.Errorf("validRuleLibName(%d chars) = %v; want %v", len(c.name), got, c.want)
		}
	}
}

// TestApplyRuleLibReadCurrentError covers the asset target blocked by a same-named directory: reading the current state fails,
// and neither a write nor a reload happens.
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

// TestRollbackTempCreateFails covers the rollback temp file landing in a directory-blocked path: it surfaces
// "write rollback temp" rather than silently losing the old content.
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

// reloadFailOnce fails the first call and succeeds after: simulating a reload failure recovered by the rollback reload.
func reloadFailOnce(n *int) func() error {
	return func() error {
		*n++
		if *n == 1 {
			return errors.New("reload boom")
		}
		return nil
	}
}

// TestApplyReloadRecoversViaRollback covers the main config reload failing but the rollback succeeding: the old config
// is restored, the ack is marked Reverted and the error uses the rolled-back wording.
func TestApplyReloadRecoversViaRollback(t *testing.T) {
	spec := testSpec(t, "")
	d := New(log.New(io.Discard, "", 0))
	if err := os.WriteFile(spec.ConfigPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	ack := d.Apply(spec, pushFor("new"), reloadFailOnce(&calls))
	if ack.OK || !ack.Reverted {
		t.Fatalf("want revert, got %+v", ack)
	}
	if !strings.Contains(ack.Error, "(rolled back)") || !strings.Contains(ack.Error, "reload boom") {
		t.Fatalf("ack = %+v", ack)
	}
	if calls != 2 {
		t.Fatalf("reload calls = %d, want 2", calls)
	}
	if got := readFile(t, spec.ConfigPath); got != "old" {
		t.Fatalf("config = %q, want old", got)
	}
}

// TestApplyRuleLibReloadRecoversViaRollback covers the rule library reload failing but the rollback succeeding:
// the new file is removed when there is no old file, and the error uses the rolled-back wording as well.
func TestApplyRuleLibReloadRecoversViaRollback(t *testing.T) {
	spec := testSpec(t, "")
	spec.AssetDir = filepath.Join(t.TempDir(), "xray-assets")
	d := New(log.New(io.Discard, "", 0))

	push := pushFor("geo-data")
	push.Kind = "rulelib:geosite.dat"
	calls := 0
	ack := d.Apply(spec, push, reloadFailOnce(&calls))
	if ack.OK || !ack.Reverted {
		t.Fatalf("want revert, got %+v", ack)
	}
	if !strings.Contains(ack.Error, "(rolled back)") {
		t.Fatalf("ack = %+v", ack)
	}
	if calls != 2 {
		t.Fatalf("reload calls = %d, want 2", calls)
	}
	if _, err := os.Stat(filepath.Join(spec.AssetDir, "geosite.dat")); !os.IsNotExist(err) {
		t.Fatalf("rolled-back rulelib should be removed: %v", err)
	}
}

// roDir creates a read-only directory (injection point for write failures) and restores permissions at test end so t.TempDir can clean up.
func roDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	return dir
}

// TestApplyWriteFailures covers config write-chain failure semantics: a directory that cannot be created/
// a temp file that cannot be written / a replacement target that is a directory all raise explicit errors rather than claiming success (reload is never triggered).
func TestApplyWriteFailures(t *testing.T) {
	d := New(log.New(io.Discard, "", 0))
	calls := 0
	// the directory cannot be created: the parent is read-only, so the child MkdirAll fails.
	spec := config.ProcSpec{Name: "xray", Kind: "xray",
		ConfigPath: filepath.Join(roDir(t), "sub", "xray.json")}
	if ack := d.Apply(spec, pushFor("{}"), reloadOK(&calls)); ack.OK ||
		!strings.Contains(ack.Error, "prepare config dir") {
		t.Fatalf("ack = %+v; want prepare config dir failure", ack)
	}
	// the temp file cannot be written: the config directory is read-only.
	spec.ConfigPath = filepath.Join(t.TempDir(), "ro", "xray.json")
	ro := filepath.Dir(spec.ConfigPath)
	if err := os.Mkdir(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	if ack := d.Apply(spec, pushFor("{}"), reloadOK(&calls)); ack.OK ||
		!strings.Contains(ack.Error, "write temp config") {
		t.Fatalf("ack = %+v; want write temp config failure", ack)
	}
	// (the rename failure branch is unreachable on Linux: a directory target already fails first at readIfExists
	// with EISDIR, see the twelfth-batch review record, so it is not contrived here.)
	if calls != 0 {
		t.Fatalf("reload calls = %d; want 0 (failures must not reload)", calls)
	}
}

// TestApplyRuleLibFailures covers rule library write-chain failure semantics: the asset directory cannot be created/
// the temp file cannot be written / the target is a directory: all raise explicit errors rather than claiming success.
func TestApplyRuleLibFailures(t *testing.T) {
	d := New(log.New(io.Discard, "", 0))
	calls := 0
	push := rulelibPush("geoip.dat", "geo-data")
	// the asset directory cannot be created: the parent is read-only.
	spec := config.ProcSpec{Name: "xray", Kind: "xray",
		ConfigPath: filepath.Join(t.TempDir(), "xray.json"),
		AssetDir:   filepath.Join(roDir(t), "assets")}
	if ack := d.Apply(spec, push, reloadOK(&calls)); ack.OK ||
		!strings.Contains(ack.Error, "prepare asset dir") {
		t.Fatalf("ack = %+v; want prepare asset dir failure", ack)
	}
	// the temp file cannot be written: the asset directory is read-only (MkdirAll passes when it exists, CreateTemp refuses).
	spec.AssetDir = roDir(t)
	if ack := d.Apply(spec, push, reloadOK(&calls)); ack.OK ||
		!strings.Contains(ack.Error, "write rulelib temp") {
		t.Fatalf("ack = %+v; want write rulelib temp failure", ack)
	}
	// (the rename failure branch is the same as TestApplyWriteFailures: readIfExists already reports EISDIR,
	// unreachable on Linux, so it is not contrived.)
	if calls != 0 {
		t.Fatalf("reload calls = %d; want 0 (failures must not reload)", calls)
	}
}

// TestRollbackPermissionFailures covers rollback failure semantics: the new file cannot be removed / the temp
// file cannot be written: the error is raised explicitly.
func TestRollbackPermissionFailures(t *testing.T) {
	d := &Deployer{}
	ro := t.TempDir()
	target := filepath.Join(ro, "new.json")
	if err := os.WriteFile(target, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })

	// no old file: the read-only directory prevents removing the new file.
	if err := d.rollback(target, nil, false, func() error { return nil }, ".tmp-*"); err == nil ||
		!strings.Contains(err.Error(), "remove new file") {
		t.Fatalf("err = %v; want remove new file failure", err)
	}
	// with an old file: the read-only directory prevents writing the rollback temp file.
	if err := d.rollback(target, []byte("old"), true, func() error { return nil }, ".tmp-*"); err == nil ||
		!strings.Contains(err.Error(), "write rollback temp") {
		t.Fatalf("err = %v; want write rollback temp failure", err)
	}
}

// TestRollbackRenameFails covers rename unable to overwrite the target when restoring the old content (target is a directory):
// the error surfaces and the temp residue is cleaned up.
func TestRollbackRenameFails(t *testing.T) {
	d := &Deployer{}
	dir := t.TempDir()
	target := filepath.Join(dir, "dirtarget")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	loads := 0
	if err := d.rollback(target, []byte("old"), true, reloadOK(&loads), ".tmp-*"); err == nil ||
		!strings.Contains(err.Error(), "restore file") {
		t.Fatalf("err = %v; want restore file failure", err)
	}
	if loads != 0 {
		t.Fatalf("reload calls = %d; want 0", loads)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("dir has %d entries err=%v; want only target", len(entries), err)
	}
}
