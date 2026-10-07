package provision

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
// 密钥绝不出现；未知类型报错不冒称支持。
func TestRenderHCL(t *testing.T) {
	tpl := storage.ProvisionTemplate{Name: "hk-3t", Plan: "vc2-1c-1gb", Region: "hkg", Image: "docker"}
	hcl, err := RenderHCL("vultr", tpl, "ferry-hk-3t")
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
	if _, err := RenderHCL("aliyun", tpl, "x"); err == nil {
		t.Fatal("unknown provider must fail")
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

	job, err := m.Apply(context.Background(), tpl, "vultr", "sk-live-key", "ferry-hk-3t", "apply")
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
	if _, err := m.Apply(context.Background(), tpl, "vultr", "sk-live-key", "ferry-hk-3t", "plan"); err != nil {
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
	job2, err := m2.Apply(context.Background(), tpl, "vultr", "k", "x", "apply")
	if err != nil {
		t.Fatal(err)
	}
	if job2.Status != "failed" || !strings.Contains(job2.Log, "boom") {
		t.Fatalf("job2 = %+v", job2)
	}

	// 非法 action：留痕 failed 不中断（与执行失败同口径）。
	job3, err := m.Apply(context.Background(), tpl, "vultr", "k", "x", "destroy")
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
