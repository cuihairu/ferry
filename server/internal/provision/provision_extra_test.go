package provision

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPrepareLaunchValidation rejects on any one of the three gaps: credential entry, provider ciphertext, token issuance.
func TestPrepareLaunchValidation(t *testing.T) {
	db := newTestDB(t)
	m := New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "ok", nil
	})
	store := secret.NewStore("l-master")

	// Credential entry not configured.
	_, err := m.PrepareLaunch(LaunchParams{InstanceName: "x"})
	if err == nil || !strings.Contains(err.Error(), "secret master key not configured") {
		t.Fatalf("no store err = %v", err)
	}

	// Provider has no ciphertext.
	_, err = m.PrepareLaunch(LaunchParams{InstanceName: "x", Store: store,
		Provider: storage.Provider{Name: "vultr"}})
	if err == nil || !strings.Contains(err.Error(), "no access key") {
		t.Fatalf("no access key err = %v", err)
	}

	// Token issuer not injected.
	sealed, err := store.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.PrepareLaunch(LaunchParams{InstanceName: "x", Store: store,
		Provider: storage.Provider{AccessKey: sealed}})
	if err == nil || !strings.Contains(err.Error(), "token issuer not provided") {
		t.Fatalf("no token issuer err = %v", err)
	}
}

// TestPrepareLaunchTokenError token issuance fails: pre-issuance aborts and keeps the reason.
func TestPrepareLaunchTokenError(t *testing.T) {
	db := newTestDB(t)
	m := New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "ok", nil
	})
	store := secret.NewStore("l-master")
	sealed, err := store.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.PrepareLaunch(LaunchParams{InstanceName: "fresh-1", Store: store,
		Provider: storage.Provider{AccessKey: sealed},
		NewToken: func() (string, error) { return "", errors.New("token boom") }})
	if err == nil || !strings.Contains(err.Error(), "provision: prepare node") ||
		!strings.Contains(err.Error(), "token boom") {
		t.Fatalf("err = %v; want wrapped token error", err)
	}
}

// TestPrepareLaunchReusesNode reuse of a same-named node: the token does not churn and no duplicate row is created.
func TestPrepareLaunchReusesNode(t *testing.T) {
	db := newTestDB(t)
	m := New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "ok", nil
	})
	store := secret.NewStore("l-master")
	sealed, err := store.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	seed := storage.Node{Name: "dup", Port: 443, Protocol: "vless", Enabled: true,
		Token: "keep-me", Status: "provisioning"}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatal(err)
	}
	prep, err := m.PrepareLaunch(LaunchParams{InstanceName: "dup", Store: store,
		Provider: storage.Provider{Name: "vultr", Type: "vultr", AccessKey: sealed},
		NewToken: func() (string, error) { return "", errors.New("must not be called on reuse") }})
	if err != nil {
		t.Fatal(err)
	}
	if prep.Node.Token != "keep-me" {
		t.Fatalf("reused token = %q; want keep-me", prep.Node.Token)
	}
	var n int64
	if err := db.Model(&storage.Node{}).Where("name = ?", "dup").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("dup nodes = %d; want reuse without new row", n)
	}
	if prep.Job == nil || prep.Job.Status != "running" {
		t.Fatalf("job = %+v; want running", prep.Job)
	}
}

// TestCompactJSON normalizes config templates: empty falls back to {}, non-JSON passes through, valid JSON is compacted.
func TestCompactJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "{}"},
		{"   ", "{}"},
		{"{oops", "{oops"},
		{`{"a": 1, "b": [2, 3]}`, `{"a":1,"b":[2,3]}`},
	}
	for _, c := range cases {
		if got := compactJSON(c.in); got != c.want {
			t.Fatalf("compactJSON(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestExecuteFailureBranches the working directory cannot be created, HCL rendering fails, main.tf cannot be written:
// all three branches end up traced as failed.
func TestExecuteFailureBranches(t *testing.T) {
	db := newTestDB(t)
	tpl := storage.ProvisionTemplate{Name: "hk", Plan: "vc2-1c-1gb", Region: "hkg"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	okRunner := func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "ok", nil
	}

	// The root path is occupied by a file: the directory cannot be created.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(db, "tofu", filepath.Join(blocker, "nope"), okRunner)
	status, log := m.Execute(context.Background(), execParams(tpl, "vultr", "k", "x", "plan"))
	if status != "failed" || !strings.Contains(log, "mkdir workdir") {
		t.Fatalf("mkdir = %s %q", status, log)
	}

	// Unknown provider type: it fails right at rendering.
	m = New(db, "tofu", t.TempDir(), okRunner)
	status, log = m.Execute(context.Background(), execParams(tpl, "aliyun", "k", "x", "plan"))
	if status != "failed" || !strings.Contains(log, "not supported yet") {
		t.Fatalf("render = %s %q", status, log)
	}

	// The main.tf path is occupied by a directory: it cannot be written.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("tpl-%d", tpl.ID), "main.tf"), 0o755); err != nil {
		t.Fatal(err)
	}
	m = New(db, "tofu", root, okRunner)
	status, log = m.Execute(context.Background(), execParams(tpl, "vultr", "k", "x", "plan"))
	if status != "failed" || !strings.Contains(log, "write main.tf") {
		t.Fatalf("write = %s %q", status, log)
	}
}

// TestNewDefaultsBinAndInitFailure an unset bin falls back to tofu: when init cannot run it is
// traced as failed with an init prefix.
func TestNewDefaultsBinAndInitFailure(t *testing.T) {
	db := newTestDB(t)
	tpl := storage.ProvisionTemplate{Name: "hk", Plan: "p", Region: "r"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	m := New(db, "", t.TempDir(), nil) // run empty = a real exec
	status, log := m.Execute(context.Background(), execParams(tpl, "vultr", "k", "x", "plan"))
	if status != "failed" || !strings.Contains(log, "init:") {
		t.Fatalf("init failure = %s %q", status, log)
	}
}

// TestErrStringAndTailLog conventions for the error prefix and the log-tail truncation.
func TestErrStringAndTailLog(t *testing.T) {
	if errString(nil) != "" {
		t.Fatalf("errString(nil) = %q", errString(nil))
	}
	if got := errString(errors.New("x")); got != "\nx" {
		t.Fatalf("errString = %q", got)
	}
	long := strings.Repeat("x", logTail+100)
	if got := tailLog(long); len(got) != logTail {
		t.Fatalf("tailLog length = %d; want %d", len(got), logTail)
	}
	if tailLog("short") != "short" {
		t.Fatalf("tailLog short = %q", tailLog("short"))
	}
}

// TestReplenishLoggerDefaultAndInFlight the sweep stands down while the pool is not empty (which also covers the
// logger default); an in-flight apply job likewise prevents launching a new machine.
func TestReplenishLoggerDefaultAndInFlight(t *testing.T) {
	r, _ := replenishFix(t, true)
	db := r.Manager.DB()
	// The pool has one usable entry: no trigger, and a nil logger uses the default.
	seed := storage.Node{Name: "pool-seed", Port: 443, Protocol: "vless", Enabled: true,
		Token: "t-seed", Status: "online", Role: "entry", PoolState: pool.StateActive}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatal(err)
	}
	fired, err := r.Sweep(context.Background(), nil)
	if err != nil || fired {
		t.Fatalf("sweep with non-empty pool = %v, %v; want false nil", fired, err)
	}

	// The pool is empty but there is an in-flight apply job: no action.
	if err := db.Where("1=1").Delete(&storage.Node{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.ProvisionJob{TemplateID: 1, TemplateName: "x",
		Action: "apply", Status: "running", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	fired, err = r.Sweep(context.Background(), nil)
	if err != nil || fired {
		t.Fatalf("sweep with in-flight job = %v, %v; want false nil", fired, err)
	}
}

// TestReplenishTemplateProviderMissing the template points at a nonexistent provider: it errors rather than claiming success.
func TestReplenishTemplateProviderMissing(t *testing.T) {
	r, tpl := replenishFix(t, true)
	db := r.Manager.DB()
	if err := db.Model(tpl).Update("provider_id", 99999).Error; err != nil {
		t.Fatal(err)
	}
	_, err := r.Sweep(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "replenish: template provider missing") {
		t.Fatalf("err = %v", err)
	}
}

// TestReplenishPrepareLaunchError the credential entry is not configured: the PrepareLaunch failure is propagated as-is.
func TestReplenishPrepareLaunchError(t *testing.T) {
	r, _ := replenishFix(t, true)
	r.Store = nil
	_, err := r.Sweep(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "secret master key not configured") {
		t.Fatalf("err = %v", err)
	}
}

// TestReplenishLoopDefaultsAndErrorLog the loop's logger/interval defaults and the fact that
// an in-round failure is only logged and does not stop the loop.
func TestReplenishLoopDefaultsAndErrorLog(t *testing.T) {
	// A Replenisher with no template configured: each round spins idle and exits when ctx fires (logger/interval defaults).
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	ReplenishLoop(ctx, &Replenisher{}, 0, nil)

	// Template provider missing: each round errors, only logged, not fatal.
	r, tpl := replenishFix(t, true)
	if err := r.Manager.DB().Model(tpl).Update("provider_id", 99999).Error; err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	ctx2, cancel2 := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel2()
	ReplenishLoop(ctx2, r, 0, log.New(&buf, "", 0))
	if !strings.Contains(buf.String(), "replenish sweep") {
		t.Fatalf("sweep error not logged: %q", buf.String())
	}
}
