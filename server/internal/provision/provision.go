// Package provision 是供给执行层（OS-2）：机型模板渲染 HCL、调 tofu CLI
// 受控执行 plan/apply。底座 = OpenTofu（不自研供给引擎、不逐家封 SDK）；
// ferry 只做模板渲染与执行编排。P1 先通 vultr 一家，多 provider 横向扩。
//
// 凭证安全（R24）：云 API 密钥经 TF_VAR 环境变量传入，不写进 HCL、
// 不进日志、不进 state 快照之外的面板留痕；state 集中存面板侧工作目录。
package provision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// logTail 是 job 留痕保留的日志尾部上限。
const logTail = 8 * 1024

// Runner 是一次 tofu 命令执行：cwd 工作目录、args 命令行、env 附加环境变量，
// 返回合并输出。可注入替代实现供测试。
type Runner func(ctx context.Context, dir string, args []string, env map[string]string) (string, error)

// Manager 编排供给执行：工作目录管理、HCL 渲染落盘、tofu 受控执行与留痕。
// 全局串行（单实例不并发 apply）。
type Manager struct {
	db     *gorm.DB
	bin    string // tofu 可执行文件路径
	root   string // 工作目录根，每模板一目录（state 集中在目录内）
	mu     sync.Mutex
	run    Runner
}

// New 创建管理器；run 空=真实 exec tofu。
func New(db *gorm.DB, bin, root string, run Runner) *Manager {
	if bin == "" {
		bin = "tofu"
	}
	if run == nil {
		run = realRunner
	}
	return &Manager{db: db, bin: bin, root: root, run: run}
}

func realRunner(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
	cmd := exec.CommandContext(ctx, "tofu", args...)
	cmd.Dir = dir
	for k, v := range env {
		cmd.Env = append(os.Environ(), k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RenderHCL 由模板渲染工作目录的 main.tf。P1 先通 vultr 一家；密钥
// 不入 HCL（走 TF_VAR_api_key）。未知 provider 类型报错，不冒称支持。
func RenderHCL(providerType string, tpl storage.ProvisionTemplate, instanceName string) (string, error) {
	switch providerType {
	case "vultr":
		return fmt.Sprintf(`terraform {
  required_providers {
    vultr = { source = "vultr/vultr" }
  }
}

variable "api_key" { type = string, sensitive = true }

provider "vultr" { api_key = var.api_key }

resource "vultr_instance" "node" {
  plan   = %q
  region = %q
  label  = %q
  %s
}
`, tpl.Plan, tpl.Region, instanceName, imageLineVultr(tpl.Image)), nil
	default:
		return "", fmt.Errorf("provision: provider type %q not supported yet (vultr only for now)", providerType)
	}
}

func imageLineVultr(image string) string {
	if image == "" {
		return "" // 走 provider 缺省镜像
	}
	return fmt.Sprintf("image_id = %q", image)
}

// Apply 留痕执行：建 running job → Execute → 回填终态。
func (m *Manager) Apply(ctx context.Context, tpl storage.ProvisionTemplate, providerType, apiKey, instanceName string, action string) (*storage.ProvisionJob, error) {
	job := storage.ProvisionJob{
		TemplateID: tpl.ID, TemplateName: tpl.Name, Action: action, Status: "running",
	}
	if err := m.db.Create(&job).Error; err != nil {
		return nil, err
	}
	status, log := m.Execute(ctx, tpl, providerType, apiKey, instanceName, action)
	now := time.Now()
	job.Status, job.Log, job.FinishedAt = status, log, &now
	m.db.Model(&storage.ProvisionJob{}).Where("id = ?", job.ID).
		Updates(map[string]any{"status": status, "log": log, "finished_at": now})
	return &job, nil
}

// Execute 同步执行一轮供给（渲染 → init → plan/apply），返回终态与日志尾部，
// 不留痕（留痕由调用方负责，便于复用已有 job 行）。
func (m *Manager) Execute(ctx context.Context, tpl storage.ProvisionTemplate, providerType, apiKey, instanceName, action string) (string, string) {
	if action != "plan" && action != "apply" {
		return "failed", "provision: action must be plan/apply"
	}
	failed := func(log string) (string, string) { return "failed", tailLog(log) }

	dir := filepath.Join(m.root, fmt.Sprintf("tpl-%d", tpl.ID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return failed("mkdir workdir: " + err.Error())
	}
	hcl, err := RenderHCL(providerType, tpl, instanceName)
	if err != nil {
		return failed(err.Error())
	}
	tfPath := filepath.Join(dir, "main.tf")
	if old, err := os.ReadFile(tfPath); err != nil || string(old) != hcl {
		if err := os.WriteFile(tfPath, []byte(hcl), 0o600); err != nil {
			return failed("write main.tf: " + err.Error())
		}
	}

	// 单实例不并发：全局串行（plan 也排队，避免 state 读写交错）。
	m.mu.Lock()
	defer m.mu.Unlock()

	env := map[string]string{"TF_VAR_api_key": apiKey}
	if out, err := m.run(ctx, dir, []string{"init", "-input=false", "-no-color"}, env); err != nil {
		return failed("init: " + out + errString(err))
	}
	args := []string{action, "-input=false", "-no-color", "-auto-approve"}
	if action == "plan" {
		args = []string{"plan", "-input=false", "-no-color"}
	}
	out, err := m.run(ctx, dir, args, env)
	if err != nil {
		return failed(strings.Join(args, " ") + ": " + out + errString(err))
	}
	return "ok", tailLog(out)
}

// DecryptKey 由提供商记录解出 API 密钥（仅 server 执行供给时使用，不落日志）。
func DecryptKey(p storage.Provider, store *secret.Store) (string, error) {
	if p.AccessKey == "" {
		return "", errors.New("provision: provider has no access key")
	}
	return store.Decrypt(p.AccessKey)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return "\n" + err.Error()
}

func tailLog(s string) string {
	if len(s) <= logTail {
		return s
	}
	return s[len(s)-logTail:]
}
