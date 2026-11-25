// Package provision is the supply execution layer (OS-2): it renders machine templates to HCL
// and drives the tofu CLI for controlled plan/apply. The foundation is OpenTofu (no home-grown supply
// engine, no per-vendor SDK wrappers); ferry only does template rendering and execution orchestration. P1 gets one vendor (vultr) through first, then scales out to more providers.
//
// Credential security (R24): cloud API keys are passed in as TF_VAR environment variables, never written
// into HCL, never logged, never left in panel traces beyond the state snapshot; state is stored centrally in the panel-side working directory.
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

	"github.com/cuihairu/ferry/server/internal/safestr"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// logTail is the upper bound of log tail kept for job tracing.
const logTail = 8 * 1024

// Runner is one tofu command execution: dir is the working directory, args the command line, env the
// extra environment variables; it returns the combined output. An alternative implementation can be injected for tests.
type Runner func(ctx context.Context, dir string, args []string, env map[string]string) (string, error)

// Manager orchestrates supply execution: working-directory management, rendering HCL to disk,
// controlled tofu execution and tracing. Globally serial (a single instance never applies concurrently).
type Manager struct {
	db   *gorm.DB
	bin  string // path to the tofu executable
	root string // root of the working directories, one directory per template (state kept inside that directory)
	mu   sync.Mutex
	run  Runner
}

// New creates the manager; an empty run means really exec tofu.
func New(db *gorm.DB, bin, root string, run Runner) *Manager {
	if bin == "" {
		bin = "tofu"
	}
	if run == nil {
		run = realRunner
	}
	return &Manager{db: db, bin: bin, root: root, run: run}
}

// sharedManagers registers Manager singletons by (db, bin, root). Execute relies on m.mu for its
// "single instance never concurrent" promise, but previously the HTTP manual trigger and the recovery/
// replenishment loop each called New, so their locks were unrelated -- concurrent init/apply in the same
// workdir would trample the terraform state. Shared makes the whole process share one Manager for the same db, tofu and root, serializing at process granularity.
var (
	sharedMu       sync.Mutex
	sharedManagers = map[sharedKey]*Manager{}
)

type sharedKey struct {
	db   *gorm.DB
	bin  string
	root string
}

// Shared returns the shared manager for (db, bin, root), with the same parameter conventions as New.
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

// DB exposes the underlying database connection for the orchestrator (such as recovery pipeline L3) to run pre-launch queries.
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

// ExecParams is the full input for one supply execution (apply carries cloud-init from OS-3 on).
type ExecParams struct {
	Template     storage.ProvisionTemplate
	ProviderType string
	APIKey       string
	InstanceName string
	Action       string
	// UserData is the cloud-init user_data (rendered by CloudInit); empty means inject nothing (not needed by plan).
	UserData string
}

// RenderHCL renders main.tf in the working directory from the template. P1 gets one vendor (vultr) through
// first; keys do not enter HCL (they go via TF_VAR_api_key). An unknown provider type errors out rather than
// claiming support. When userData is non-empty it is injected into vultr_instance.user_data via a heredoc
// (the provider base64-encodes it itself before handing it to the Vultr API); the content is written flush left to avoid the indentation stripping of an escaped heredoc.
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
		return "" // use the provider's default image
	}
	return fmt.Sprintf("image_id = %q", image)
}

// Apply executes with tracing: create a running job -> Execute -> write back the terminal state.
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

// Execute synchronously runs one round of supply (render -> init -> plan/apply) and returns the terminal state
// plus the log tail, without tracing (the caller handles tracing, so an existing job row can be reused).
func (m *Manager) Execute(ctx context.Context, p ExecParams) (string, string) {
	action := p.Action
	if action != "plan" && action != "apply" {
		return "failed", "provision: action must be plan/apply"
	}
	failed := func(log string) (string, string) { return "failed", tailLog(log) }

	// Single instance never concurrent: the whole round is serial (including rendering main.tf to disk; plan queues too).
	// The lock must cover the HCL write -- otherwise a concurrent apply+plan on the same template lets the later
	// writer overwrite the earlier one's main.tf, and whoever got the lock first runs the wrong HCL (apply picks up the plan template = cloud-init lost).
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

// DecryptKey extracts the API key from the provider record (used only when the server executes supply; never logged).
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

// tailLog takes the trailing segment of the execution log for tracing: the start point is pulled back to a UTF-8
// character boundary (tofu output can contain Chinese; a hard byte cut would split a character).
func tailLog(s string) string {
	return safestr.Tail(s, logTail)
}
