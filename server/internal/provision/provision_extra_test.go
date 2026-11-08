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

// TestPrepareLaunchValidation 凭证入口/供应商密文/令牌签发三档缺一即拒绝。
func TestPrepareLaunchValidation(t *testing.T) {
	db := newTestDB(t)
	m := New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "ok", nil
	})
	store := secret.NewStore("l-master")

	// 凭证入口未配。
	_, err := m.PrepareLaunch(LaunchParams{InstanceName: "x"})
	if err == nil || !strings.Contains(err.Error(), "secret master key not configured") {
		t.Fatalf("no store err = %v", err)
	}

	// 供应商没有密文。
	_, err = m.PrepareLaunch(LaunchParams{InstanceName: "x", Store: store,
		Provider: storage.Provider{Name: "vultr"}})
	if err == nil || !strings.Contains(err.Error(), "no access key") {
		t.Fatalf("no access key err = %v", err)
	}

	// 令牌签发器未注入。
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

// TestPrepareLaunchTokenError 令牌签发失败：预签发中止并留原因。
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

// TestPrepareLaunchReusesNode 同名节点复用：token 不抖动、不重复建行。
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

// TestCompactJSON 配置模板规整：空回落 {}、非 JSON 原样、合法 JSON 压紧。
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

// TestExecuteFailureBranches 工作目录建不了、HCL 渲染失败、main.tf 写不进：
// 三档都以 failed 留痕收场。
func TestExecuteFailureBranches(t *testing.T) {
	db := newTestDB(t)
	tpl := storage.ProvisionTemplate{Name: "hk", Plan: "vc2-1c-1gb", Region: "hkg"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	okRunner := func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "ok", nil
	}

	// 根路径被文件占住：目录建不了。
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(db, "tofu", filepath.Join(blocker, "nope"), okRunner)
	status, log := m.Execute(context.Background(), execParams(tpl, "vultr", "k", "x", "plan"))
	if status != "failed" || !strings.Contains(log, "mkdir workdir") {
		t.Fatalf("mkdir = %s %q", status, log)
	}

	// 未知供应商类型：渲染即败。
	m = New(db, "tofu", t.TempDir(), okRunner)
	status, log = m.Execute(context.Background(), execParams(tpl, "aliyun", "k", "x", "plan"))
	if status != "failed" || !strings.Contains(log, "not supported yet") {
		t.Fatalf("render = %s %q", status, log)
	}

	// main.tf 位置被目录占住：写不进。
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

// TestNewDefaultsBinAndInitFailure bin 未给回落 tofu：init 跑不起来时以
// failed 留痕并带 init 前缀。
func TestNewDefaultsBinAndInitFailure(t *testing.T) {
	db := newTestDB(t)
	tpl := storage.ProvisionTemplate{Name: "hk", Plan: "p", Region: "r"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	m := New(db, "", t.TempDir(), nil) // run 空=真实 exec
	status, log := m.Execute(context.Background(), execParams(tpl, "vultr", "k", "x", "plan"))
	if status != "failed" || !strings.Contains(log, "init:") {
		t.Fatalf("init failure = %s %q", status, log)
	}
}

// TestErrStringAndTailLog 错误前缀与日志尾段截断口径。
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

// TestReplenishLoggerDefaultAndInFlight 池未空时不动手（顺带覆盖 logger
// 缺省）；在途 apply job 同样阻止开新机。
func TestReplenishLoggerDefaultAndInFlight(t *testing.T) {
	r, _ := replenishFix(t, true)
	db := r.Manager.DB()
	// 池有一台可用入口：不触发，logger nil 走缺省。
	seed := storage.Node{Name: "pool-seed", Port: 443, Protocol: "vless", Enabled: true,
		Token: "t-seed", Status: "online", Role: "entry", PoolState: pool.StateActive}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatal(err)
	}
	fired, err := r.Sweep(context.Background(), nil)
	if err != nil || fired {
		t.Fatalf("sweep with non-empty pool = %v, %v; want false nil", fired, err)
	}

	// 池空但有在途 apply job：不动手。
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

// TestReplenishTemplateProviderMissing 模板指向不存在的供应商：报错不冒称。
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

// TestReplenishPrepareLaunchError 凭证入口未配：PrepareLaunch 失败原样上抛。
func TestReplenishPrepareLaunchError(t *testing.T) {
	r, _ := replenishFix(t, true)
	r.Store = nil
	_, err := r.Sweep(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "secret master key not configured") {
		t.Fatalf("err = %v", err)
	}
}

// TestReplenishLoopDefaultsAndErrorLog loop 的 logger/interval 缺省与
// 轮内失败只记日志继续。
func TestReplenishLoopDefaultsAndErrorLog(t *testing.T) {
	// 未配置模板的 Replenisher：每轮空转，ctx 到点退出（logger/interval 缺省）。
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	ReplenishLoop(ctx, &Replenisher{}, 0, nil)

	// 模板供应商缺失：每轮报错，只记日志不中断。
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
