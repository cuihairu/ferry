package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// stubExec 拦截重启：记录调用并恢复默认。
func stubExec(t *testing.T) *[]string {
	t.Helper()
	calls := &[]string{}
	old := execFn
	execFn = func(self string) { *calls = append(*calls, self) }
	t.Cleanup(func() { execFn = old })
	return calls
}

// writeFakeBin 写一个可执行假二进制，返回路径与内容哈希。
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
	// 原二进制未被替换
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
	// 新二进制就位、备份与待验证标记存在、触发重启
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

	// Commit：标记与备份清除
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

	// 模拟升级后状态：新二进制 + 备份 + 待标记
	if err := os.WriteFile(bakPath(self), []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatalf("write bak: %v", err)
	}
	if err := os.WriteFile(pendingPath(self), []byte(time.Now().Format(time.RFC3339)), 0o644); err != nil {
		t.Fatalf("write pending: %v", err)
	}

	// 超时未 Commit → 回滚到备份并重启
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
	Commit(self) // 验证通过，标记清除

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
