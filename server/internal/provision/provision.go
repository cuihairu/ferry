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
	db   *gorm.DB
	bin  string // tofu 可执行文件路径
	root string // 工作目录根，每模板一目录（state 集中在目录内）
	mu   sync.Mutex
	run  Runner
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

// sharedManagers 按 (db, bin, root) 登记 Manager 单例。Execute 靠 m.mu
// 承诺「单实例不并发」，但此前 HTTP 手动触发与恢复/补充循环各自 New，
// 锁互不相干——同 workdir 并发 init/apply 会踩烂 terraform state。
// Shared 让同库同 tofu 同根目录全进程共用一个 Manager，串行以进程为界。
var (
	sharedMu       sync.Mutex
	sharedManagers = map[sharedKey]*Manager{}
)

type sharedKey struct {
	db   *gorm.DB
	bin  string
	root string
}

// Shared 返回 (db, bin, root) 对应的共享管理器，参数口径与 New 一致。
func Shared(db *gorm.DB, bin, root string, run Runner) *Manager {
	if bin == "" {
		bin = "tofu"
	}
	k := sharedKey{db: db, bin: bin, root: root}
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if m, ok := sharedManagers[k]; ok {
		return m
	}
	m := New(db, bin, root, run)
	sharedManagers[k] = m
	return m
}

// DB 暴露底层库连接，供编排方（如恢复流水线 L3）做开服前置查询。
func (m *Manager) DB() *gorm.DB { return m.db }

func realRunner(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
	cmd := exec.CommandContext(ctx, "tofu", args...)
	cmd.Dir = dir
	for k, v := range env {
		cmd.Env = append(os.Environ(), k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ExecParams 是一次供给执行的全部输入（OS-3 起 apply 携带 cloud-init）。
type ExecParams struct {
	Template     storage.ProvisionTemplate
	ProviderType string
	APIKey       string
	InstanceName string
	Action       string
	// UserData 是 cloud-init user_data（CloudInit 渲染）；空=不注入（plan 无需）。
	UserData string
}

// RenderHCL 由模板渲染工作目录的 main.tf。P1 先通 vultr 一家；密钥
// 不入 HCL（走 TF_VAR_api_key）。未知 provider 类型报错，不冒称支持。
// userData 非空时经 heredoc 注入 vultr_instance.user_data（provider 侧
// 自行 base64 后交 Vultr API）；内容顶格写，避开转义 heredoc 的缩进剥离。
func RenderHCL(providerType string, tpl storage.ProvisionTemplate, instanceName, userData string) (string, error) {
	switch providerType {
	case "vultr":
		var b strings.Builder
		fmt.Fprintf(&b, `terraform {
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
`, tpl.Plan, tpl.Region, instanceName)
		if img := imageLineVultr(tpl.Image); img != "" {
			b.WriteString("  " + img + "\n")
		}
		if userData != "" {
			b.WriteString("  user_data = <<EOT\n" + strings.TrimRight(userData, "\n") + "\nEOT\n")
		}
		b.WriteString("}\n")
		return b.String(), nil
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
func (m *Manager) Apply(ctx context.Context, p ExecParams) (*storage.ProvisionJob, error) {
	job := storage.ProvisionJob{
		TemplateID: p.Template.ID, TemplateName: p.Template.Name, Action: p.Action, Status: "running",
	}
	if err := m.db.Create(&job).Error; err != nil {
		return nil, err
	}
	status, log := m.Execute(ctx, p)
	now := time.Now()
	job.Status, job.Log, job.FinishedAt = status, log, &now
	m.db.Model(&storage.ProvisionJob{}).Where("id = ?", job.ID).
		Updates(map[string]any{"status": status, "log": log, "finished_at": now})
	return &job, nil
}

// Execute 同步执行一轮供给（渲染 → init → plan/apply），返回终态与日志尾部，
// 不留痕（留痕由调用方负责，便于复用已有 job 行）。
func (m *Manager) Execute(ctx context.Context, p ExecParams) (string, string) {
	action := p.Action
	if action != "plan" && action != "apply" {
		return "failed", "provision: action must be plan/apply"
	}
	failed := func(log string) (string, string) { return "failed", tailLog(log) }

	// 单实例不并发：整轮串行（含 main.tf 渲染落盘，plan 也排队）。锁必须
	// 罩住 HCL 写——否则同模板并发 apply+plan 时后写者覆盖前者 main.tf，
	// 先获锁者会跑错 HCL（apply 拿到 plan 模板=丢 cloud-init）。
	m.mu.Lock()
	defer m.mu.Unlock()

	dir := filepath.Join(m.root, fmt.Sprintf("tpl-%d", p.Template.ID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return failed("mkdir workdir: " + err.Error())
	}
	hcl, err := RenderHCL(p.ProviderType, p.Template, p.InstanceName, p.UserData)
	if err != nil {
		return failed(err.Error())
	}
	tfPath := filepath.Join(dir, "main.tf")
	if old, err := os.ReadFile(tfPath); err != nil || string(old) != hcl {
		if err := os.WriteFile(tfPath, []byte(hcl), 0o600); err != nil {
			return failed("write main.tf: " + err.Error())
		}
	}

	env := map[string]string{"TF_VAR_api_key": p.APIKey}
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
