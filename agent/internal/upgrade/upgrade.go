// Package upgrade 执行 agent 自升级（A-23）：下载新二进制 → 校验 → 备份 →
// 替换 → 重启；启动验证超时（未连上面板）自动回滚到备份。
package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// RollbackTimeout 是启动验证窗口：重启后超时仍未连上面板则回滚。
const RollbackTimeout = 120 * time.Second

// downloadTimeout 覆盖大二进制下载的最坏耗时。
const downloadTimeout = 5 * time.Minute

// Self 返回自身可执行文件的真实路径（解析符号链接）。
func Self() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

func bakPath(self string) string { return self + ".bak" }

func pendingPath(self string) string { return self + ".pending" }

// Pending 是否存在待验证标记（上次升级后未完成启动验证）。
func Pending(self string) bool {
	_, err := os.Stat(pendingPath(self))
	return err == nil
}

// Commit 启动验证通过：删除待验证标记与备份。
func Commit(self string) {
	_ = os.Remove(pendingPath(self))
	_ = os.Remove(bakPath(self))
}

// Rollback 恢复备份并删除标记（启动验证失败时调用）。
func Rollback(self string) error {
	if err := os.Remove(pendingPath(self)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove pending marker: %w", err)
	}
	if _, err := os.Stat(bakPath(self)); err == nil {
		if err := os.Rename(bakPath(self), self); err != nil {
			return fmt.Errorf("restore backup: %w", err)
		}
	}
	return nil
}

// Watchdog 启动验证看门狗：timeout 内未 Commit 则回滚并重启旧二进制。
func Watchdog(self string, timeout time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	<-time.After(timeout)
	if !Pending(self) {
		return // 已 Commit，新二进制验证通过
	}
	logger.Printf("upgrade verify timeout (%s), rolling back to backup", timeout)
	if err := Rollback(self); err != nil {
		logger.Printf("rollback failed: %v", err)
		return
	}
	Exec(self)
}

// Exec 以自身路径重新 exec（替换进程镜像，参数与环境不变）。
// execFn 可注入（测试用），默认 syscall.Exec 重启自身。
var execFn = func(self string) {
	if err := syscall.Exec(self, os.Args, os.Environ()); err != nil {
		log.Printf("re-exec %s: %v", self, err)
		os.Exit(1)
	}
}

// Exec 执行重启（经 execFn，测试可拦截）。
func Exec(self string) {
	execFn(self)
}

// Do 执行升级全流程：下载 → 校验 → 备份 → 替换 → 写标记 → 重启。
// 任一步失败返回错误，不改动现有二进制。
func Do(ctx context.Context, self string, up agentproto.Upgrade, logger *log.Logger) error {
	if logger == nil {
		logger = log.Default()
	}
	if up.URL == "" {
		return errors.New("upgrade url is required")
	}
	if _, err := os.Stat(self); err != nil {
		return fmt.Errorf("self binary: %w", err)
	}

	// 下载到同目录临时文件（rename 原子替换要求同文件系统）。
	tmp, err := os.CreateTemp(filepath.Dir(self), ".upgrade-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 成功替换后临时文件已不存在

	client := &http.Client{Timeout: downloadTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, up.URL, nil)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		return fmt.Errorf("download: status %d", resp.StatusCode)
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("download body: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	// 校验和（面板提供时才校验）。
	if up.Sha256 != "" {
		sum, err := sha256File(tmpName)
		if err != nil {
			return fmt.Errorf("sha256: %w", err)
		}
		if sum != up.Sha256 {
			return fmt.Errorf("sha256 mismatch: got %s want %s", sum, up.Sha256)
		}
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	// 备份当前二进制（清旧备份），再原子替换。
	_ = os.Remove(bakPath(self))
	if err := os.Rename(self, bakPath(self)); err != nil {
		return fmt.Errorf("backup self: %w", err)
	}
	if err := os.Rename(tmpName, self); err != nil {
		// 替换失败：恢复备份，保持原状。
		_ = os.Rename(bakPath(self), self)
		return fmt.Errorf("swap binary: %w", err)
	}

	// 写待验证标记后重启；新进程连上面板（Commit）或超时回滚。
	if err := os.WriteFile(pendingPath(self), []byte(time.Now().Format(time.RFC3339)), 0o644); err != nil {
		return fmt.Errorf("write pending marker: %w", err)
	}
	logger.Printf("upgrading to %s, restarting", up.Version)
	Exec(self)
	return nil
}

// sha256File 计算文件 SHA-256（十六进制）。
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
