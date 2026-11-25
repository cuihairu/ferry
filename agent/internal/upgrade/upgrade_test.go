package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// stubExec intercepts the restart: it records the call and restores the default.
func stubExec(t *testing.T) *[]string {
	t.Helper()
	calls := &[]string{}
	old := execFn
	execFn = func(self string) { *calls = append(*calls, self) }
	t.Cleanup(func() { execFn = old })
	return calls
}

// writeFakeBin writes an executable fake binary and returns its path and content hash.
func writeFakeBin(t *testing.T, dir, name, content string) (string, string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}
	sum := sha256.Sum256([]byte(content))
	return p, hex.EncodeToString(sum[:])
}

func TestDoDownloadFail(t *testing.T) {
	self, _ := writeFakeBin(t, t.TempDir(), "agent", "#!/bin/sh\n")
	calls := stubExec(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := Do(context.Background(), self, agentproto.Upgrade{Version: "v2", URL: srv.URL}, nil)
	if err == nil {
		t.Fatal("download 404 should fail")
	}
	if len(*calls) != 0 {
		t.Fatalf("no exec on failure, calls=%v", *calls)
	}
	if Pending(self) {
		t.Fatal("no pending marker expected on failure")
	}
}

func TestDoSha256Mismatch(t *testing.T) {
	self, _ := writeFakeBin(t, t.TempDir(), "agent", "#!/bin/sh\n")
	calls := stubExec(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#!/bin/sh\necho new\n"))
	}))
	defer srv.Close()

	err := Do(context.Background(), self, agentproto.Upgrade{
		Version: "v2", URL: srv.URL, Sha256: "000000000000000000000000000000000000000000000000000000000000000",
	}, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "sha256 mismatch") {
		t.Fatalf("sha mismatch should fail: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("no exec on mismatch, calls=%v", *calls)
	}
	// the original binary was not replaced
	got, _ := os.ReadFile(self)
	if string(got) != "#!/bin/sh\n" {
		t.Fatalf("self binary must stay intact: %q", got)
	}
}

func TestDoSuccess(t *testing.T) {
	dir := t.TempDir()
	self, _ := writeFakeBin(t, dir, "agent", "#!/bin/sh\n")
	calls := stubExec(t)

	newContent := "#!/bin/sh\necho v2\n"
	newSum := sha256.Sum256([]byte(newContent))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(newContent))
	}))
	defer srv.Close()

	err := Do(context.Background(), self, agentproto.Upgrade{
		Version: "v2", URL: srv.URL, Sha256: hex.EncodeToString(newSum[:]),
	}, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	// the new binary is in place, the backup and pending marker exist, and the restart fired
	got, _ := os.ReadFile(self)
	if string(got) != "#!/bin/sh\necho v2\n" {
		t.Fatalf("self binary not swapped: %q", got)
	}
	if !Pending(self) {
		t.Fatal("pending marker expected")
	}
	if _, err := os.Stat(bakPath(self)); err != nil {
		t.Fatalf("backup expected: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0] != self {
		t.Fatalf("exec once with self: %v", *calls)
	}

	// Commit: the marker and backup are cleared
	Commit(self)
	if Pending(self) {
		t.Fatal("pending marker should be cleared after commit")
	}
	if _, err := os.Stat(bakPath(self)); !os.IsNotExist(err) {
		t.Fatal("backup should be removed after commit")
	}
}

func TestWatchdogRollback(t *testing.T) {
	dir := t.TempDir()
	self, _ := writeFakeBin(t, dir, "agent", "#!/bin/sh\n")
	calls := stubExec(t)

	// simulating the post-upgrade state: a new binary + a backup + the pending marker
	if err := os.WriteFile(bakPath(self), []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatalf("write bak: %v", err)
	}
	if err := os.WriteFile(pendingPath(self), []byte(time.Now().Format(time.RFC3339)), 0o644); err != nil {
		t.Fatalf("write pending: %v", err)
	}

	// no Commit before the timeout -> roll back to the backup and restart
	Watchdog(self, 50*time.Millisecond, nil)
	if len(*calls) != 1 {
		t.Fatalf("rollback should re-exec once: %v", *calls)
	}
	got, _ := os.ReadFile(self)
	if string(got) != "#!/bin/sh\necho old\n" {
		t.Fatalf("rollback should restore backup: %q", got)
	}
	if Pending(self) {
		t.Fatal("pending marker should be cleared after rollback")
	}
}

func TestWatchdogCommitted(t *testing.T) {
	dir := t.TempDir()
	self, _ := writeFakeBin(t, dir, "agent", "#!/bin/sh\n")
	calls := stubExec(t)

	if err := os.WriteFile(pendingPath(self), []byte(time.Now().Format(time.RFC3339)), 0o644); err != nil {
		t.Fatalf("write pending: %v", err)
	}
	Commit(self) // verification passed and the marker is cleared

	Watchdog(self, 50*time.Millisecond, nil)
	if len(*calls) != 0 {
		t.Fatalf("committed upgrade must not roll back: %v", *calls)
	}
}

func TestSelf(t *testing.T) {
	self, err := Self()
	if err != nil {
		t.Fatalf("Self: %v", err)
	}
	if self == "" || self[0] != '/' {
		t.Fatalf("Self should be absolute path: %q", self)
	}
}

func TestDoMissingURL(t *testing.T) {
	self, _ := writeFakeBin(t, t.TempDir(), "agent", "#!/bin/sh\n")
	stubExec(t)
	if err := Do(context.Background(), self, agentproto.Upgrade{Version: "v2"}, nil); err == nil {
		t.Fatal("missing url should fail")
	}
}

func TestDoBadDownloadURL(t *testing.T) {
	self, _ := writeFakeBin(t, t.TempDir(), "agent", "#!/bin/sh\n")
	stubExec(t)
	err := Do(context.Background(), self, agentproto.Upgrade{
		Version: "v2", URL: "http://127.0.0.1:1/nope",
	}, nil)
	if err == nil {
		t.Fatal("unreachable url should fail")
	}
	if Pending(self) {
		t.Fatal("no pending marker on failure")
	}
}

func TestDoMalformedURL(t *testing.T) {
	self, _ := writeFakeBin(t, t.TempDir(), "agent", "#!/bin/sh\n")
	stubExec(t)
	// the URL cannot build a request: it should stop at building it, touching no network and replacing no binary.
	err := Do(context.Background(), self, agentproto.Upgrade{
		Version: "v2", URL: "http://%zz",
	}, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "build request") {
		t.Fatalf("malformed url should fail at request build: %v", err)
	}
	if got, _ := os.ReadFile(self); string(got) != "#!/bin/sh\n" {
		t.Fatalf("self binary must stay intact: %q", got)
	}
}

func TestDoSelfMissing(t *testing.T) {
	stubExec(t)
	self := filepath.Join(t.TempDir(), "absent-agent")
	err := Do(context.Background(), self, agentproto.Upgrade{
		Version: "v2", URL: "http://127.0.0.1:1/nope",
	}, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "self binary") {
		t.Fatalf("missing self should fail first: %v", err)
	}
}

// TestDoBackupRenameFails covers the backup path being blocked by a non-empty directory: the replacement aborts and the original binary stays.
func TestDoBackupRenameFails(t *testing.T) {
	dir := t.TempDir()
	self, _ := writeFakeBin(t, dir, "agent", "#!/bin/sh\n")
	stubExec(t)
	// os.Remove fails on a non-empty directory (and is ignored); the subsequent rename onto the non-empty directory -> fails.
	if err := os.MkdirAll(filepath.Join(bakPath(self), "keep"), 0o755); err != nil {
		t.Fatalf("mkdir blocker: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#!/bin/sh\necho v2\n"))
	}))
	defer srv.Close()

	err := Do(context.Background(), self, agentproto.Upgrade{Version: "v2", URL: srv.URL}, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "backup self") {
		t.Fatalf("blocked backup should fail: %v", err)
	}
	if got, _ := os.ReadFile(self); string(got) != "#!/bin/sh\n" {
		t.Fatalf("self binary must stay intact: %q", got)
	}
	if Pending(self) {
		t.Fatal("no pending marker when backup fails")
	}
}

// TestRollbackPendingRemoveError covers the pending-marker path being blocked by a non-empty directory: rollback errors and never touches the binary.
func TestRollbackPendingRemoveError(t *testing.T) {
	dir := t.TempDir()
	self, _ := writeFakeBin(t, dir, "agent", "#!/bin/sh\n")
	if err := os.MkdirAll(filepath.Join(pendingPath(self), "keep"), 0o755); err != nil {
		t.Fatalf("mkdir blocker: %v", err)
	}
	err := Rollback(self)
	if err == nil || !strings.HasPrefix(err.Error(), "remove pending marker") {
		t.Fatalf("err = %v; want remove pending marker failure", err)
	}
	if got, _ := os.ReadFile(self); string(got) != "#!/bin/sh\n" {
		t.Fatalf("self binary must stay intact: %q", got)
	}
}

// TestWatchdogRollbackFailure covers a failed rollback: log only, no restart (stay on the new version and wait for manual handling).
func TestWatchdogRollbackFailure(t *testing.T) {
	dir := t.TempDir()
	self, _ := writeFakeBin(t, dir, "agent", "#!/bin/sh\n")
	calls := stubExec(t)
	if err := os.MkdirAll(filepath.Join(pendingPath(self), "keep"), 0o755); err != nil {
		t.Fatalf("mkdir blocker: %v", err)
	}

	Watchdog(self, 50*time.Millisecond, log.New(io.Discard, "", 0))
	if len(*calls) != 0 {
		t.Fatalf("failed rollback must not re-exec: %v", *calls)
	}
}

// TestRollbackWithoutBackup covers clearing only the marker with no backup to restore: a normal return.
func TestRollbackWithoutBackup(t *testing.T) {
	self, _ := writeFakeBin(t, t.TempDir(), "agent", "#!/bin/sh\n")
	if err := os.WriteFile(pendingPath(self), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(self); err != nil {
		t.Fatalf("Rollback without backup = %v; want nil", err)
	}
	if Pending(self) {
		t.Fatal("pending marker should be cleared")
	}
	if got, _ := os.ReadFile(self); string(got) != "#!/bin/sh\n" {
		t.Fatalf("self must stay: %q", got)
	}
}

func TestSha256FileMissing(t *testing.T) {
	if _, err := sha256File(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("missing file should error")
	}
}
