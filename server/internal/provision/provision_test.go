package provision

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestRenderHCL covers vultr rendering: plan/region/label land in the HCL, the image is optional,
// keys never appear; userData is injected as a heredoc; an unknown type errors rather than claiming support.
func TestRenderHCL(t *testing.T) {
	tpl := storage.ProvisionTemplate{Name: "hk-3t", Plan: "vc2-1c-1gb", Region: "hkg", Image: "docker"}
	hcl, err := RenderHCL("vultr", tpl, "ferry-hk-3t", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`plan   = "vc2-1c-1gb"`, `region = "hkg"`, `label  = "ferry-hk-3t"`, `image_id = "docker"`} {
		if !strings.Contains(hcl, want) {
			t.Fatalf("hcl missing %q:\n%s", want, hcl)
		}
	}
	// Keys only appear as a variable reference (injected as TF_VAR_api_key), never as a literal.
	if !strings.Contains(hcl, "api_key = var.api_key") || strings.Contains(hcl, "sk-") {
		t.Fatalf("hcl must reference var only, no literal key:\n%s", hcl)
	}
	if strings.Contains(hcl, "user_data") {
		t.Fatalf("no userData must mean no user_data attr:\n%s", hcl)
	}
	if _, err := RenderHCL("aliyun", tpl, "x", ""); err == nil {
		t.Fatal("unknown provider must fail")
	}

	// userData (OS-3): the heredoc is injected flush left, and the #cloud-config first line is preserved as-is.
	hcl2, err := RenderHCL("vultr", tpl, "ferry-hk-3t", "#cloud-config\nwrite_files: []\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hcl2, "user_data = <<EOT\n#cloud-config\nwrite_files: []\nEOT\n") {
		t.Fatalf("userData heredoc missing or mangled:\n%s", hcl2)
	}
}

// TestCloudInit covers cloud-init rendering (OS-3): the #cloud-config first line, agent.json
// carrying token/panel_url via base64, the systemd unit and install script in place, and the download
// URL assembled from the parameters; the token does not appear in plaintext in the rendered output.
func TestCloudInit(t *testing.T) {
	cfg, err := AgentConfigJSON(AgentConfigFile{PanelURL: "wss://panel.example.com/agent/ws", AgentID: "hk-1", Token: "tok_abc"})
	if err != nil {
		t.Fatal(err)
	}
	ud := CloudInit(cfg, "https://dl.example.com/ferry/", "1.2.3")
	if !strings.HasPrefix(ud, "#cloud-config\n") {
		t.Fatalf("missing #cloud-config header:\n%s", ud)
	}
	// agent.json is injected base64-encoded (the token is never plaintext) and can be decoded back with all fields intact.
	if strings.Contains(ud, "tok_abc") {
		t.Fatalf("token must not appear in plain text:\n%s", ud)
	}
	wantCfg := base64.StdEncoding.EncodeToString([]byte(cfg))
	if !strings.Contains(ud, "content: "+wantCfg) {
		t.Fatalf("agent.json b64 content missing:\n%s", ud)
	}
	decoded, err := base64.StdEncoding.DecodeString(wantCfg)
	if err != nil || string(decoded) != cfg {
		t.Fatalf("b64 roundtrip failed: %q err=%v", decoded, err)
	}
	for _, want := range []string{
		"path: /etc/ferry/agent.json",
		"path: /etc/systemd/system/ferry-agent.service",
		"ExecStart=/usr/local/bin/ferry-agent -config /etc/ferry/agent.json",
		"Restart=always",
		"https://dl.example.com/ferry/v1.2.3/ferry-agent_1.2.3_linux_${arch}.tar.gz",
		"uname -m", "x86_64) arch=amd64", "aarch64|arm64) arch=arm64",
		"systemctl restart ferry-agent",
	} {
		if !strings.Contains(ud, want) {
			t.Fatalf("cloud-init missing %q:\n%s", want, ud)
		}
	}
	// A trailing slash on the base URL is normalized: no double-slash version numbers.
	if strings.Contains(ud, "//v1.") {
		t.Fatalf("base trailing slash not trimmed:\n%s", ud)
	}
}

// TestPanelWSURL covers the convention from panel base URL to agent ws address (consistent with agent-install.sh).
func TestPanelWSURL(t *testing.T) {
	cases := map[string]string{
		"http://localhost:8080":      "ws://localhost:8080/agent/ws",
		"https://panel.example.com/": "wss://panel.example.com/agent/ws",
		"ws://x:1":                   "ws://x:1/agent/ws",
		"wss://x":                    "wss://x/agent/ws",
		"panel.example.com":          "ws://panel.example.com/agent/ws",
	}
	for in, want := range cases {
		if got := PanelWSURL(in); got != want {
			t.Fatalf("PanelWSURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestApplyLifecycle covers execution orchestration (with a fake runner injected): the init->action order,
// keys only via the TF_VAR environment variable, main.tf not rewritten when its content is unchanged, terminal state traced as ok/failed.
func TestApplyLifecycle(t *testing.T) {
	db := newTestDB(t)
	tpl := storage.ProvisionTemplate{Name: "hk-3t", Plan: "vc2-1c-1gb", Region: "hkg", Image: "docker"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}

	var calls [][]string
	var seenEnv map[string]string
	root := t.TempDir()
	m := New(db, "tofu", root, func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		calls = append(calls, args)
		seenEnv = env
		return "planned", nil
	})

	job, err := m.Apply(context.Background(), execParams(tpl, "vultr", "sk-live-key", "ferry-hk-3t", "apply"))
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "ok" || job.FinishedAt == nil {
		t.Fatalf("job = %+v", job)
	}
	if len(calls) != 2 || calls[0][0] != "init" || calls[1][0] != "apply" {
		t.Fatalf("calls = %v", calls)
	}
	if seenEnv["TF_VAR_api_key"] != "sk-live-key" {
		t.Fatalf("env = %v", seenEnv)
	}
	if strings.Contains(job.Log, "sk-live-key") {
		t.Fatal("job log must not contain api key")
	}

	// main.tf is written and matches the render; re-running with unchanged content does not rewrite it (mtime unchanged).
	tfPath := filepath.Join(root, "tpl-"+strconv.Itoa(int(tpl.ID)), "main.tf")
	first, err := os.ReadFile(tfPath)
	if err != nil {
		t.Fatal(err)
	}
	info1, _ := os.Stat(tfPath)
	if _, err := m.Apply(context.Background(), execParams(tpl, "vultr", "sk-live-key", "ferry-hk-3t", "plan")); err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Stat(tfPath)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("unchanged main.tf must not be rewritten")
	}
	_ = first

	// runner fails -> traced as failed.
	m2 := New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		if args[0] == "apply" {
			return "boom", context.DeadlineExceeded
		}
		return "inited", nil
	})
	job2, err := m2.Apply(context.Background(), execParams(tpl, "vultr", "k", "x", "apply"))
	if err != nil {
		t.Fatal(err)
	}
	if job2.Status != "failed" || !strings.Contains(job2.Log, "boom") {
		t.Fatalf("job2 = %+v", job2)
	}

	// An illegal action: traced as failed without aborting (same convention as an
	// execution failure). destroy is legal since F-1658 (the cleanup leg), so a
	// made-up action stands in for the illegal case.
	job3, err := m.Apply(context.Background(), execParams(tpl, "vultr", "k", "x", "reap"))
	if err != nil || job3.Status != "failed" {
		t.Fatalf("invalid action job = %+v err=%v", job3, err)
	}
}

// TestDecryptKey covers the secret-decryption convention: no ciphertext and a wrong key both error.
func TestDecryptKey(t *testing.T) {
	store := secret.NewStore("k1")
	sealed, err := store.Encrypt("sk-x")
	if err != nil {
		t.Fatal(err)
	}
	if k, err := DecryptKey(storage.Provider{AccessKey: sealed}, store); err != nil || k != "sk-x" {
		t.Fatalf("key = %q err=%v", k, err)
	}
	if _, err := DecryptKey(storage.Provider{}, store); err == nil {
		t.Fatal("empty key must fail")
	}
	if _, err := DecryptKey(storage.Provider{AccessKey: "v1:bad"}, secret.NewStore("k2")); err == nil {
		t.Fatal("wrong key must fail")
	}
}

// execParams is a convenience constructor for tests.
func execParams(tpl storage.ProvisionTemplate, providerType, apiKey, instanceName, action string) ExecParams {
	return ExecParams{Template: tpl, ProviderType: providerType, APIKey: apiKey, InstanceName: instanceName, Action: action}
}

// TestSharedManagerSingleton: one instance per (db,bin,root) across the whole process -- the HTTP manual
// trigger and the recovery/replenishment loop share one lock, so the serialization promise is bounded by
// the process (previously each called New with unrelated locks, and concurrent init/apply in the same workdir would trample the terraform state).
func TestSharedManagerSingleton(t *testing.T) {
	db := newTestDB(t)
	root := t.TempDir()
	a := Shared(db, "tofu", root, nil)
	if b := Shared(db, "tofu", root, nil); b != a {
		t.Fatal("same (db,bin,root) must return one shared manager")
	}
	if c := Shared(db, "tofu", t.TempDir(), nil); c == a {
		t.Fatal("different root must not share")
	}
	if d := Shared(newTestDB(t), "tofu", root, nil); d == a {
		t.Fatal("different db must not share")
	}
	// An empty bin is normalized to tofu and then shares under the same key (consistent with New).
	if e := Shared(db, "", root, nil); e != a {
		t.Fatal("empty bin normalizes to tofu and shares")
	}
}

// TestSharedManagerSerializes: many goroutines call Execute on the same instance via Shared,
// and the peak concurrency inside the runner must be 1 (process-level serialization).
func TestSharedManagerSerializes(t *testing.T) {
	db := newTestDB(t)
	tpl := storage.ProvisionTemplate{Name: "hk-3t", Plan: "vc2-1c-1gb", Region: "hkg", Image: "docker"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	var cur, peak int32
	m := Shared(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		c := atomic.AddInt32(&cur, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if c <= p || atomic.CompareAndSwapInt32(&peak, p, c) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&cur, -1)
		return "planned", nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.Execute(context.Background(), ExecParams{
				Template: tpl, ProviderType: "vultr", APIKey: "sk-test",
				InstanceName: "inst", Action: "plan",
			})
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&peak) != 1 {
		t.Fatalf("concurrent execute peak = %d, want 1", atomic.LoadInt32(&peak))
	}
}

// TestExecuteHCLIsolation: concurrent apply+plan on the same template, and the main.tf read by each tofu
// run must be the version rendered for that action (apply carries user_data, plan does not).
// Regression background: the HCL render-to-disk used to happen outside the lock, so a later writer overwrote
// the earlier one's main.tf and whoever took the lock first ran the wrong HCL (apply picked up the plan template = cloud-init lost).
func TestExecuteHCLIsolation(t *testing.T) {
	db := newTestDB(t)
	tpl := storage.ProvisionTemplate{Name: "hk-3t", Plan: "vc2-1c-1gb", Region: "hkg", Image: "docker"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[string]string{}
	m := New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		if args[0] == "plan" || args[0] == "apply" {
			b, err := os.ReadFile(filepath.Join(dir, "main.tf"))
			if err != nil {
				return "", err
			}
			mu.Lock()
			seen[args[0]] = string(b)
			mu.Unlock()
			time.Sleep(20 * time.Millisecond) // widen the interleaving window
		}
		return "ok", nil
	})
	for round := 0; round < 5; round++ {
		var wg sync.WaitGroup
		for _, a := range []string{"apply", "plan"} {
			wg.Add(1)
			go func(action string) {
				defer wg.Done()
				p := execParams(tpl, "vultr", "sk-test", "inst", action)
				if action == "apply" {
					p.UserData = "#cloud-config\nruncmd: []\n"
				}
				m.Execute(context.Background(), p)
			}(a)
		}
		wg.Wait()
	}
	if !strings.Contains(seen["apply"], "user_data") {
		t.Fatalf("apply run saw wrong HCL (no user_data):\n%s", seen["apply"])
	}
	if strings.Contains(seen["plan"], "user_data") {
		t.Fatalf("plan run saw apply HCL:\n%s", seen["plan"])
	}
}

// TestRealRunnerCancelledCtx the real runner with an already-cancelled ctx: it fails as soon as it starts the
// process, without depending on whether tofu is installed on the machine.
func TestRealRunnerCancelledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := realRunner(ctx, t.TempDir(), []string{"version"}, map[string]string{"TF_IN_AUTOMATION": "1"})
	if err == nil {
		t.Fatalf("realRunner with cancelled ctx = %q, nil; want error", out)
	}
}

// TestExecuteDestroyAction covers the F-1658 cleanup leg: the destroy action
// reaches the runner with -auto-approve, an unknown action is refused by the
// guard (so a typo can never execute anything).
func TestExecuteDestroyAction(t *testing.T) {
	db := newTestDB(t)
	tpl := storage.ProvisionTemplate{Name: "hk-3t", Plan: "vc2-1c-1gb", Region: "hkg"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	var calls []string
	m := New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		calls = append(calls, args[0])
		return "ok", nil
	})
	status, _ := m.Execute(context.Background(), execParams(tpl, "vultr", "sk-test", "inst", "destroy"))
	if status != "ok" {
		t.Fatalf("destroy status = %s", status)
	}
	if len(calls) != 2 || calls[0] != "init" || calls[1] != "destroy" {
		t.Fatalf("calls = %v", calls)
	}
	status, msg := m.Execute(context.Background(), execParams(tpl, "vultr", "sk-test", "inst", "bogus"))
	if status != "failed" || !strings.Contains(msg, "plan/apply/destroy") {
		t.Fatalf("bogus action = %s %q", status, msg)
	}
	if len(calls) != 2 {
		t.Fatalf("bogus action must not reach the runner: %v", calls)
	}
}
