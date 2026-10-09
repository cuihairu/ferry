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

// TestRenderHCL 覆盖 vultr 渲染：plan/region/label 落 HCL、镜像可选、
// 密钥绝不出现；userData 注入 heredoc；未知类型报错不冒称支持。
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
	// 密钥只经变量引用（TF_VAR_api_key 注入），绝不出现字面量。
	if !strings.Contains(hcl, "api_key = var.api_key") || strings.Contains(hcl, "sk-") {
		t.Fatalf("hcl must reference var only, no literal key:\n%s", hcl)
	}
	if strings.Contains(hcl, "user_data") {
		t.Fatalf("no userData must mean no user_data attr:\n%s", hcl)
	}
	if _, err := RenderHCL("aliyun", tpl, "x", ""); err == nil {
		t.Fatal("unknown provider must fail")
	}

	// userData（OS-3）：heredoc 顶格注入，#cloud-config 首行原样保留。
	hcl2, err := RenderHCL("vultr", tpl, "ferry-hk-3t", "#cloud-config\nwrite_files: []\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hcl2, "user_data = <<EOT\n#cloud-config\nwrite_files: []\nEOT\n") {
		t.Fatalf("userData heredoc missing or mangled:\n%s", hcl2)
	}
}

// TestCloudInit 覆盖 cloud-init 渲染（OS-3）：#cloud-config 首行、agent.json
// 经 base64 携带 token/panel_url、systemd unit 与安装脚本落位、下载地址按
// 参数拼装；token 不以明文出现在渲染结果里。
func TestCloudInit(t *testing.T) {
	cfg, err := AgentConfigJSON(AgentConfigFile{PanelURL: "wss://panel.example.com/agent/ws", AgentID: "hk-1", Token: "tok_abc"})
	if err != nil {
		t.Fatal(err)
	}
	ud := CloudInit(cfg, "https://dl.example.com/ferry/", "1.2.3")
	if !strings.HasPrefix(ud, "#cloud-config\n") {
		t.Fatalf("missing #cloud-config header:\n%s", ud)
	}
	// agent.json 经 base64 注入（token 不落明文），可解回且字段齐全。
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
	// 基址尾斜杠归一：不出双斜杠版本号。
	if strings.Contains(ud, "//v1.") {
		t.Fatalf("base trailing slash not trimmed:\n%s", ud)
	}
}

// TestPanelWSURL 覆盖面板基址到 agent ws 地址的口径（与 agent-install.sh 一致）。
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

// TestApplyLifecycle 覆盖执行编排（注入 fake runner）：init→action 顺序、
// 密钥只经 TF_VAR 环境变量、main.tf 内容不变不重写、终态留痕 ok/failed。
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

	// main.tf 落盘且与渲染一致；复跑内容不变不重写（mtime 不变）。
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

	// runner 失败 → failed 留痕。
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

	// 非法 action：留痕 failed 不中断（与执行失败同口径）。
	job3, err := m.Apply(context.Background(), execParams(tpl, "vultr", "k", "x", "destroy"))
	if err != nil || job3.Status != "failed" {
		t.Fatalf("invalid action job = %+v err=%v", job3, err)
	}
}

// TestDecryptKey 覆盖机密解密口径：无密文与错钥报错。
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

// execParams 测试便捷构造。
func execParams(tpl storage.ProvisionTemplate, providerType, apiKey, instanceName, action string) ExecParams {
	return ExecParams{Template: tpl, ProviderType: providerType, APIKey: apiKey, InstanceName: instanceName, Action: action}
}

// TestSharedManagerSingleton：同 (db,bin,root) 全进程一个实例——HTTP 手动
// 触发与恢复/补充循环共用同一把锁，串行承诺才以进程为界（此前各自 New
// 锁互不相干，同 workdir 并发 init/apply 会踩烂 terraform state）。
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
	// 空 bin 归一 tofu 后同键共享（与 New 口径一致）。
	if e := Shared(db, "", root, nil); e != a {
		t.Fatal("empty bin normalizes to tofu and shares")
	}
}

// TestSharedManagerSerializes：多 goroutine 经 Shared 同一实例并发 Execute，
// runner 内并发峰值必须为 1（process 级串行）。
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

// TestExecuteHCLIsolation：同模板并发 apply+plan，每次 tofu 运行读到的
// main.tf 必须是对应 action 渲染的版本（apply 带 user_data、plan 不带）。
// 回归背景：HCL 渲染落盘原先在锁外，后写者覆盖前者 main.tf，先获锁者
// 会跑错 HCL（apply 拿到 plan 模板=丢 cloud-init）。
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
			time.Sleep(20 * time.Millisecond) // 放大交叉窗口
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
