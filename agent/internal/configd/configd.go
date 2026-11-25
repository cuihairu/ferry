// Package configd carries out config pushes: sha256 verification -> temp file -> validation command -> atomic replacement -> reload,
// rolling back the old configuration when validation or reload fails (A-16/A-17).
package configd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

const validateTimeout = 30 * time.Second

// Deployer applies config pushes, running only one at a time (serialization avoids a half-new half-old state).
type Deployer struct {
	log *log.Logger

	mu sync.Mutex
}

// New creates a deployer.
func New(logger *log.Logger) *Deployer {
	if logger == nil {
		logger = log.Default()
	}
	return &Deployer{log: logger}
}

// Apply performs one config push and returns the ack. The reload function is injected by the caller (the reload command of procs.Manager).
func (d *Deployer) Apply(spec config.ProcSpec, push agentproto.ConfigPush, reload func() error) agentproto.ConfigAck {
	d.mu.Lock()
	defer d.mu.Unlock()

	ack := agentproto.ConfigAck{Proc: push.Proc, Version: push.Version}
	// Rule library data files (SAVE-1): kind=rulelib:<name>, written to the asset directory rather than the main config path,
	// exempt from validate (binary data) with reload as usual; never written back to the main config path.
	if strings.HasPrefix(push.Kind, "rulelib:") {
		name := strings.TrimPrefix(push.Kind, "rulelib:")
		if !validRuleLibName(name) {
			ack.Error = fmt.Sprintf("invalid rulelib name %q", name)
			return ack
		}
		return d.applyRuleLib(spec, push, name, reload)
	}
	if spec.ConfigPath == "" {
		ack.Error = fmt.Sprintf("proc %q has no config_path", spec.Name)
		return ack
	}
	sum := sha256.Sum256([]byte(push.Payload))
	if got := hex.EncodeToString(sum[:]); got != push.Sha256 {
		ack.Error = fmt.Sprintf("sha256 mismatch: want %s got %s", push.Sha256, got)
		return ack
	}

	old, hadOld, err := readIfExists(spec.ConfigPath)
	if err != nil {
		ack.Error = fmt.Sprintf("read current config: %v", err)
		return ack
	}

	dir := filepath.Dir(spec.ConfigPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		ack.Error = fmt.Sprintf("prepare config dir: %v", err)
		return ack
	}
	tmp, err := os.CreateTemp(dir, ".ferry-config-*")
	if err != nil {
		ack.Error = fmt.Sprintf("write temp config: %v", err)
		return ack
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // if the rename already succeeded the file is gone, which is harmless
	if _, err := tmp.WriteString(push.Payload); err != nil {
		tmp.Close()
		ack.Error = fmt.Sprintf("write temp config: %v", err)
		return ack
	}
	if err := tmp.Close(); err != nil {
		ack.Error = fmt.Sprintf("write temp config: %v", err)
		return ack
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		ack.Error = fmt.Sprintf("chmod temp config: %v", err)
		return ack
	}

	// the validation command runs against the temp file; on failure the official config is left untouched.
	if spec.Validate != "" {
		if err := runValidate(spec.Validate, tmpPath); err != nil {
			ack.Error = fmt.Sprintf("validate: %v", err)
			return ack
		}
		ack.Validated = true
	}

	if err := os.Rename(tmpPath, spec.ConfigPath); err != nil {
		ack.Error = fmt.Sprintf("replace config: %v", err)
		return ack
	}

	if err := reload(); err != nil {
		// reload failed: roll back the old config and pull it once more, returning the process to the old config state.
		rbErr := d.rollback(spec.ConfigPath, old, hadOld, reload, ".ferry-config-*")
		ack.Reverted = true
		if rbErr != nil {
			ack.Error = fmt.Sprintf("reload: %v; rollback: %v", err, rbErr)
		} else {
			ack.Error = fmt.Sprintf("reload: %v (rolled back)", err)
		}
		return ack
	}

	ack.OK = true
	return ack
}

// applyRuleLib atomically lands a rule library data file in the asset directory and triggers reload; on failure the old file is rolled back.
// the directory comes from spec.AssetDir, falling back to assets/ alongside the main config when unset (aligning it with the kernel's XRAY_LOCATION_ASSET is the deploy side's job).
func (d *Deployer) applyRuleLib(spec config.ProcSpec, push agentproto.ConfigPush, name string, reload func() error) agentproto.ConfigAck {
	ack := agentproto.ConfigAck{Proc: push.Proc, Version: push.Version}
	if spec.AssetDir == "" && spec.ConfigPath == "" {
		ack.Error = fmt.Sprintf("proc %q has neither config_path nor asset_dir", spec.Name)
		return ack
	}
	sum := sha256.Sum256([]byte(push.Payload))
	if got := hex.EncodeToString(sum[:]); got != push.Sha256 {
		ack.Error = fmt.Sprintf("sha256 mismatch: want %s got %s", push.Sha256, got)
		return ack
	}
	dir := spec.AssetDir
	if dir == "" {
		dir = filepath.Join(filepath.Dir(spec.ConfigPath), "assets")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		ack.Error = fmt.Sprintf("prepare asset dir: %v", err)
		return ack
	}
	target := filepath.Join(dir, name)

	old, hadOld, err := readIfExists(target)
	if err != nil {
		ack.Error = fmt.Sprintf("read current rulelib: %v", err)
		return ack
	}
	tmp, err := os.CreateTemp(dir, ".ferry-rulelib-*")
	if err != nil {
		ack.Error = fmt.Sprintf("write rulelib temp: %v", err)
		return ack
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // gone after a successful rename, harmless
	if _, err := tmp.WriteString(push.Payload); err != nil {
		tmp.Close()
		ack.Error = fmt.Sprintf("write rulelib temp: %v", err)
		return ack
	}
	if err := tmp.Close(); err != nil {
		ack.Error = fmt.Sprintf("write rulelib temp: %v", err)
		return ack
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		ack.Error = fmt.Sprintf("chmod rulelib temp: %v", err)
		return ack
	}
	if err := os.Rename(tmpPath, target); err != nil {
		ack.Error = fmt.Sprintf("replace rulelib: %v", err)
		return ack
	}

	if err := reload(); err != nil {
		rbErr := d.rollback(target, old, hadOld, reload, ".ferry-rulelib-*")
		ack.Reverted = true
		if rbErr != nil {
			ack.Error = fmt.Sprintf("reload: %v; rollback: %v", err, rbErr)
		} else {
			ack.Error = fmt.Sprintf("reload: %v (rolled back)", err)
		}
		return ack
	}

	ack.OK = true
	return ack
}

// validRuleLibName allows only a single plain file name, preventing path traversal.
func validRuleLibName(name string) bool {
	if name == "" || len(name) > 64 || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return name[0] != '.' && name[0] != '-'
}

// rollback restores the old file content and retries reload.
func (d *Deployer) rollback(path string, old []byte, hadOld bool, reload func() error, tempPattern string) error {
	if !hadOld {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove new file: %w", err)
		}
	} else {
		tmp, err := os.CreateTemp(filepath.Dir(path), tempPattern)
		if err != nil {
			return fmt.Errorf("write rollback temp: %w", err)
		}
		tmpPath := tmp.Name()
		if _, err := tmp.Write(old); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("write rollback temp: %w", err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("write rollback temp: %w", err)
		}
		if err := os.Chmod(tmpPath, 0o644); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("chmod rollback temp: %w", err)
		}
		if err := os.Rename(tmpPath, path); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("restore file: %w", err)
		}
	}
	if err := reload(); err != nil {
		return fmt.Errorf("reload after rollback: %w", err)
	}
	return nil
}

func readIfExists(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// runValidate runs the validation command template via sh -c, with {config} replaced by the temp file path.
func runValidate(tmpl, configPath string) error {
	cmdStr := strings.ReplaceAll(tmpl, "{config}", configPath)
	ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return errors.New("validate timeout")
		}
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
