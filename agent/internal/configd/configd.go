// Package configd 落实配置下发：sha256 校验 → 临时文件 → 校验命令 → 原子替换 → reload，
// 校验或 reload 失败即回滚旧配置（A-16/A-17）。
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

// Deployer 执行配置下发，同一时间只执行一份（串行化避免半新半旧）。
type Deployer struct {
	log *log.Logger

	mu sync.Mutex
}

// New 创建下发器。
func New(logger *log.Logger) *Deployer {
	if logger == nil {
		logger = log.Default()
	}
	return &Deployer{log: logger}
}

// Apply 执行一次配置下发并返回 ack。reload 由调用方注入（procs.Manager 的 reload 指令）。
func (d *Deployer) Apply(spec config.ProcSpec, push agentproto.ConfigPush, reload func() error) agentproto.ConfigAck {
	d.mu.Lock()
	defer d.mu.Unlock()

	ack := agentproto.ConfigAck{Proc: push.Proc, Version: push.Version}
	// 规则库数据文件（SAVE-1）：kind=rulelib:<name>，落资产目录而非主配置路径，
	// 免 validate（二进制数据），reload 照常；绝不落回主配置路径。
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
	defer os.Remove(tmpPath) // 命中 rename 成功时文件已不存在，无害
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

	// 校验命令在临时文件上执行，失败不动正式配置。
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
		// reload 失败：回滚旧配置并再拉一次，让进程回到旧配置状态。
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

// applyRuleLib 把规则库数据文件原子落到资产目录并触发 reload；失败回滚旧文件。
// 目录取 spec.AssetDir，未配置时回退主配置同级的 assets/（与内核 XRAY_LOCATION_ASSET 对齐由部署侧负责）。
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
	defer os.Remove(tmpPath) // rename 成功后已不存在，无害
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

// validRuleLibName 只允许单个普通文件名：防路径穿越。
func validRuleLibName(name string) bool {
	if name == "" || len(name) > 64 || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return name[0] != '.' && name[0] != '-'
}

// rollback 恢复旧文件内容并重试 reload。
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

// runValidate 以 sh -c 执行校验命令模板，{config} 替换为临时文件路径。
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
