package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
)

// TestBackupDecryptRoundtrip 覆盖解密子命令（restore.sh 的判定依据）：正确
// 钥还原原文、-out 缺省去 .enc 后缀、错钥/未配钥/缺 -in 一律非 0。
func TestBackupDecryptRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store := secret.NewStore("mk")
	plain := []byte("SQLite format 3\x00 backup fixture")
	enc, err := store.EncryptBytes(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	in := filepath.Join(dir, "ferry-backup-x.db.enc")
	if err := os.WriteFile(in, enc, 0o600); err != nil {
		t.Fatalf("write enc: %v", err)
	}

	// 显式 -out：明文逐字节还原。
	out := filepath.Join(dir, "restored.db")
	if code := backupDecrypt([]string{"-key", "mk", "-in", in, "-out", out}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("restored mismatch: err=%v bytes=%q", err, got)
	}

	// -out 缺省：还原为去掉 .enc 后缀的同名文件。
	defIn := filepath.Join(dir, "ferry-backup-y.db.enc")
	if err := os.WriteFile(defIn, enc, 0o600); err != nil {
		t.Fatalf("write enc: %v", err)
	}
	if code := backupDecrypt([]string{"-key", "mk", "-in", defIn}); code != 0 {
		t.Fatalf("default-out exit = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "ferry-backup-y.db")); err != nil {
		t.Fatalf("default out file missing: %v", err)
	}

	// 失败面：错钥 / 未配钥（-key 空）/ 缺 -in。
	if code := backupDecrypt([]string{"-key", "wrong", "-in", in, "-out", filepath.Join(dir, "w.db")}); code == 0 {
		t.Fatal("wrong key should exit non-zero")
	}
	if code := backupDecrypt([]string{"-key", "", "-in", in}); code == 0 {
		t.Fatal("empty key should exit non-zero")
	}
	if code := backupDecrypt([]string{"-key", "mk"}); code == 0 {
		t.Fatal("missing -in should exit non-zero")
	}
}

// TestRunServesUntilCancel 覆盖装配主路径：起库、引导管理员、挂全部后台
// 循环并起 HTTP 监听；ctx 取消即优雅关停回收，不悬挂不报错。
func TestRunServesUntilCancel(t *testing.T) {
	cfg := config.Default() // Load 的缺省基线：周期字段落默认，避免零值周期
	cfg.Addr = "127.0.0.1:0"
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = filepath.Join(t.TempDir(), "ferry.db")
	cfg.BackupDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	if err := run(ctx, cfg); err != nil {
		t.Fatalf("run = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("run took %s to stop after cancel", elapsed)
	}
}

// TestRunBadDB 库打不开即冒错，不冒称起服。
func TestRunBadDB(t *testing.T) {
	cfg := config.Config{DBDriver: "bogus", DBDSN: "x"}
	err := run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "init database") {
		t.Fatalf("err = %v; want init database failure", err)
	}
}
