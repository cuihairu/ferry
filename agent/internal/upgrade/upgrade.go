// Package upgrade performs the agent's self-upgrade (A-23): download the new binary -> verify -> back up ->
// replace -> restart; if the startup verification times out (the panel is never reached) it rolls back to the backup automatically.
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

// RollbackTimeout is the startup verification window: a rollback happens if the panel is still not reached when it expires.
const RollbackTimeout = 120 * time.Second

// downloadTimeout covers the worst-case duration of downloading a large binary.
const downloadTimeout = 5 * time.Minute

// Self returns the real path of the running executable (resolving symlinks).
func Self() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

func bakPath(self string) string { return self + ".bak" }

func pendingPath(self string) string { return self + ".pending" }

// Pending reports whether the pending-verification marker exists (the startup check after the last upgrade never completed).
func Pending(self string) bool {
	_, err := os.Stat(pendingPath(self))
	return err == nil
}

// Commit records the startup verification passing: the pending marker and the backup are deleted.
func Commit(self string) {
	_ = os.Remove(pendingPath(self))
	_ = os.Remove(bakPath(self))
}

// Rollback restores the backup and deletes the marker (called when startup verification fails).
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

// Watchdog is the startup verification watchdog: without a Commit within timeout it rolls back and restarts the old binary.
func Watchdog(self string, timeout time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	<-time.After(timeout)
	if !Pending(self) {
		return // committed: the new binary passed verification
	}
	logger.Printf("upgrade verify timeout (%s), rolling back to backup", timeout)
	if err := Rollback(self); err != nil {
		logger.Printf("rollback failed: %v", err)
		return
	}
	Exec(self)
}

// Exec re-execs the process with its own path (replacing the process image, arguments and environment unchanged).
// execFn is injectable (for tests); the default syscall.Exec restarts the process itself.
var execFn = func(self string) {
	if err := syscall.Exec(self, os.Args, os.Environ()); err != nil {
		log.Printf("re-exec %s: %v", self, err)
		os.Exit(1)
	}
}

// Exec performs the restart (through execFn, interceptable in tests).
func Exec(self string) {
	execFn(self)
}

// RestartNow re-execs the current binary in place (F-1621: the panel's plain
// agent restart task). The process image is replaced, arguments and
// environment preserved; the ws connection drops and the hello re-handshake
// carries the same identity.
func RestartNow() {
	self, err := os.Executable()
	if err != nil {
		log.Printf("restart: resolve self: %v", err)
		execFn(os.Args[0])
		return
	}
	execFn(self)
}

// RollbackNow restores the backup binary (F-1626: the panel's rollback task)
// and re-execs; without a backup it reports instead of blind-restarting.
func RollbackNow(logger *log.Logger) error {
	if logger == nil {
		logger = log.Default()
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve self: %w", err)
	}
	if _, err := os.Stat(bakPath(self)); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no backup binary at %s", bakPath(self))
	}
	if err := Rollback(self); err != nil {
		return err
	}
	logger.Printf("panel-triggered rollback: restoring %s and re-exec", bakPath(self))
	execFn(self)
	return nil
}

// Do runs the whole upgrade flow: download -> verify -> back up -> replace -> write the marker -> restart.
// any step failing returns an error and leaves the current binary untouched.
func Do(ctx context.Context, self string, up agentproto.Upgrade, logger *log.Logger) error {
	return DoWithProgress(ctx, self, up, logger, nil)
}

// DoWithProgress is Do with a progress reporter (F-0712): it fires downloading/applying/applied in
// flow order and failed with the reason when any step errors. notify may be nil (plain Do).
func DoWithProgress(ctx context.Context, self string, up agentproto.Upgrade, logger *log.Logger, notify func(agentproto.UpgradeStatus)) error {
	report := func(state, errMsg string) {
		if notify == nil {
			return
		}
		notify(agentproto.UpgradeStatus{Version: up.Version, State: state, Error: errMsg})
	}
	report(agentproto.UpgradeStateDownloading, "")
	err := doUpgrade(ctx, self, up, logger, report)
	if err != nil {
		report(agentproto.UpgradeStateFailed, err.Error())
	}
	return err
}

func doUpgrade(ctx context.Context, self string, up agentproto.Upgrade, logger *log.Logger, report func(state, errMsg string)) error {
	if logger == nil {
		logger = log.Default()
	}
	if up.URL == "" {
		return errors.New("upgrade url is required")
	}
	if _, err := os.Stat(self); err != nil {
		return fmt.Errorf("self binary: %w", err)
	}

	// download into a temp file in the same directory (an atomic rename replacement requires the same filesystem).
	tmp, err := os.CreateTemp(filepath.Dir(self), ".upgrade-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // after a successful replacement the temp file is gone

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

	// checksum (verified only when the panel provides one).
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

	// back up the current binary (clearing old backups), then replace atomically.
	report(agentproto.UpgradeStateApplying, "")
	_ = os.Remove(bakPath(self))
	if err := os.Rename(self, bakPath(self)); err != nil {
		return fmt.Errorf("backup self: %w", err)
	}
	if err := os.Rename(tmpName, self); err != nil {
		// replacement failed: restore the backup, leaving everything as it was.
		_ = os.Rename(bakPath(self), self)
		return fmt.Errorf("swap binary: %w", err)
	}

	// write the pending-verification marker and restart; the new process either reaches the panel (Commit) or times out and rolls back.
	if err := os.WriteFile(pendingPath(self), []byte(time.Now().Format(time.RFC3339)), 0o644); err != nil {
		return fmt.Errorf("write pending marker: %w", err)
	}
	logger.Printf("upgrading to %s, restarting", up.Version)
	report(agentproto.UpgradeStateApplied, "")
	Exec(self)
	return nil
}

// sha256File computes a file's SHA-256 (hexadecimal).
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
