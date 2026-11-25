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

// TestBackupDecryptRoundtrip covers the decrypt subcommand (what restore.sh judges on): the right
// key restores the original, -out defaults to stripping the .enc suffix, wrong key / missing key / missing -in all exit non-zero.
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

	// Explicit -out: plaintext restored byte for byte.
	out := filepath.Join(dir, "restored.db")
	if code := backupDecrypt([]string{"-key", "mk", "-in", in, "-out", out}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("restored mismatch: err=%v bytes=%q", err, got)
	}

	// -out omitted: restored to the same file name with the .enc suffix stripped.
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

	// Failure side: wrong key / unconfigured key (empty -key) / missing -in.
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

// TestRunServesUntilCancel covers the main wiring path: opens the DB, bootstraps the admin, mounts all
// background loops and starts the HTTP listener; cancelling ctx shuts down gracefully without hanging or erroring.
func TestRunServesUntilCancel(t *testing.T) {
	cfg := config.Default() // Load's default baseline: period fields get defaults, avoiding zero-value periods
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

// TestRunBadDB: an unopenable DB surfaces the error instead of pretending to serve.
func TestRunBadDB(t *testing.T) {
	cfg := config.Config{DBDriver: "bogus", DBDSN: "x"}
	err := run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "init database") {
		t.Fatalf("err = %v; want init database failure", err)
	}
}
