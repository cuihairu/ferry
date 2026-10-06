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
		rbErr := d.rollback(spec, old, hadOld, reload)
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

// rollback 恢复旧配置内容并重试 reload。
func (d *Deployer) rollback(spec config.ProcSpec, old []byte, hadOld bool, reload func() error) error {
	if !hadOld {
		if err := os.Remove(spec.ConfigPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove new config: %w", err)
		}
	} else {
		tmp, err := os.CreateTemp(filepath.Dir(spec.ConfigPath), ".ferry-config-*")
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
		if err := os.Rename(tmpPath, spec.ConfigPath); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("restore config: %w", err)
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
