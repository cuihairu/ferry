package standby

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 面板主从同步（P2-1）测试：拉取→解密→原子替换全链、防篡改/错钥/错令
// 牌/主面板不可达四类失败不替换、关闭态与替换钩子的 Loop 语义。

// closeDB 收尾关连接池。
func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// mkSource 建源库并落一行标记，返回库文件字节（加密与否随 store）。
func mkSource(t *testing.T, store *secret.Store, mark string) []byte {
	t.Helper()
	srcPath := filepath.Join(t.TempDir(), "src.db")
	src, err := storage.Open(storage.DriverSQLite, srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Exec("CREATE TABLE mark(v TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := src.Exec("INSERT INTO mark VALUES (?)", mark).Error; err != nil {
		t.Fatal(err)
	}
	closeDB(t, src)
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if store.Enabled() {
		enc, err := store.EncryptBytes(raw)
		if err != nil {
			t.Fatal(err)
		}
		return enc
	}
	return raw
}

// mkTarget 建本机库（旧数据）供替换，返回 gorm 句柄与文件路径。
func mkTarget(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ferry.db")
	db, err := storage.Open(storage.DriverSQLite, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeDB(t, db) })
	if err := db.Exec("CREATE TABLE mark(v TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO mark VALUES ('old')").Error; err != nil {
		t.Fatal(err)
	}
	return db, path
}

// TestSyncOnceRoundtrip 覆盖全链：拉取加密快照→解密校验→替换本机库，
// 旧数据换新、侧车文件不残留。
func TestSyncOnceRoundtrip(t *testing.T) {
	store := secret.NewStore("standby-master")
	body := mkSource(t, store, "from-master")
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Standby-Token") != "tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		hits++
		w.Write(body)
	}))
	defer srv.Close()

	db, path := mkTarget(t)
	got, err := SyncOnce(Options{MasterURL: srv.URL, Token: "tok", DBPath: path}, db, store)
	if err != nil || !got {
		t.Fatalf("sync: replaced=%v err=%v", got, err)
	}
	if hits != 1 {
		t.Fatalf("hits = %d", hits)
	}
	// SyncOnce 已静默关闭旧连接池，重开验证落盘内容。
	fresh, err := storage.Open(storage.DriverSQLite, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	var v string
	if err := fresh.Raw("SELECT v FROM mark").Scan(&v).Error; err != nil {
		closeDB(t, fresh)
		t.Fatal(err)
	}
	closeDB(t, fresh)
	if v != "from-master" {
		t.Fatalf("mark = %q, want from-master（库未换新）", v)
	}
	for _, side := range []string{path + "-wal", path + "-shm", path + ".standby-tmp"} {
		if _, err := os.Stat(side); err == nil {
			t.Fatalf("sidecar left: %s", side)
		}
	}
}

// TestSyncOnceFailureModes 覆盖四类失败都不替换本机库：防篡改（非密文）、
// 错主密钥、错令牌（403）、主面板不可达。
func TestSyncOnceFailureModes(t *testing.T) {
	store := secret.NewStore("standby-master")
	other := secret.NewStore("another-key")
	body := mkSource(t, store, "from-master")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Standby-Token") != "tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()

	cases := []struct {
		name string
		url  string
		st   *secret.Store
	}{
		{name: "tampered body", url: newBodyServer(t, []byte("not a ciphertext")), st: store},
		{name: "wrong key", url: srv.URL, st: other},
	}
	// 错令牌单独跑（srv 403 分支）。
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, path := mkTarget(t)
			got, err := SyncOnce(Options{MasterURL: tc.url, Token: "tok", DBPath: path}, db, tc.st)
			if err == nil {
				t.Fatalf("%s: want error", tc.name)
			}
			if got {
				t.Fatalf("%s: must not replace", tc.name)
			}
			var v string
			if err := db.Raw("SELECT v FROM mark").Scan(&v).Error; err != nil || v != "old" {
				t.Fatalf("%s: old db unusable: %q %v", tc.name, v, err)
			}
		})
	}
	// 主面板不可达：已关闭端口的 server。
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()
	db, path := mkTarget(t)
	if got, err := SyncOnce(Options{MasterURL: dead.URL, Token: "tok", DBPath: path}, db, store); err == nil || got {
		t.Fatalf("unreachable: replaced=%v err=%v", got, err)
	}
	// 错令牌：403 分支。
	db2, path2 := mkTarget(t)
	if got, err := SyncOnce(Options{MasterURL: srv.URL, Token: "bad", DBPath: path2}, db2, store); err == nil || got {
		t.Fatalf("wrong token: replaced=%v err=%v", got, err)
	}
	// 守卫：空 URL / 空 token / 未配主密钥。
	db3, path3 := mkTarget(t)
	if _, err := SyncOnce(Options{DBPath: path3}, db3, store); err == nil {
		t.Fatal("empty url should fail")
	}
	if _, err := SyncOnce(Options{MasterURL: srv.URL, DBPath: path3}, db3, store); err == nil {
		t.Fatal("empty token should fail")
	}
	if _, err := SyncOnce(Options{MasterURL: srv.URL, Token: "tok", DBPath: path3}, db3, secret.NewStore("")); err == nil {
		t.Fatal("disabled store should fail")
	}
}

// newBodyServer 返回恒写 body 的临时 server 地址（用完随测试目录回收）。
func newBodyServer(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestLoopGuards 覆盖 Loop：MasterURL 空=未开启立即返回；成功替换触发
// OnReplace 恰一次且循环退出。
func TestLoopGuards(t *testing.T) {
	// 未开启：直接返回（tick 极短防误等）。
	Loop(context.Background(), Options{Sec: 1, tick: time.Millisecond}, nil, nil, func() {})

	store := secret.NewStore("standby-master")
	body := mkSource(t, store, "from-master")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()
	db, path := mkTarget(t)
	called := 0
	done := make(chan struct{})
	go func() {
		Loop(context.Background(),
			Options{MasterURL: srv.URL, Token: "t", DBPath: path, Sec: 60, tick: 10 * time.Millisecond},
			db, store, func() { called++ })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not return after replace")
	}
	if called != 1 {
		t.Fatalf("onReplace called %d times", called)
	}
}

// TestSQLitePath 覆盖 DSN 取路径：裸路径/file: 前缀/pragma 查询段。
func TestSQLitePath(t *testing.T) {
	for dsn, want := range map[string]string{
		"ferry.db":            "ferry.db",
		"/data/ferry.db":      "/data/ferry.db",
		"file:/data/ferry.db": "/data/ferry.db",
		"/data/ferry.db?_pragma=journal_mode(WAL)": "/data/ferry.db",
	} {
		if got := sqlitePath(dsn); got != want {
			t.Fatalf("sqlitePath(%q) = %q, want %q", dsn, got, want)
		}
	}
}

// TestDBPathFromDSNExport 导出包装与 sqlitePath 同口径（main 取本机库路径）。
func TestDBPathFromDSNExport(t *testing.T) {
	if got := DBPathFromDSN("file:/data/ferry.db?_pragma=journal_mode(WAL)"); got != "/data/ferry.db" {
		t.Fatalf("DBPathFromDSN = %q", got)
	}
}

// TestSyncOnceGuardsAndBadSnap 补齐守卫与坏快照分支：空 dbPath、非法 URL、
// 解密通过但非 sqlite 库文件（错钥外另三类拒收口）。
func TestSyncOnceGuardsAndBadSnap(t *testing.T) {
	store := secret.NewStore("standby-master")
	db, path := mkTarget(t)

	// 空 dbPath：替换无目标。
	if _, err := SyncOnce(Options{MasterURL: "http://x", Token: "t", DBPath: ""}, db, store); err == nil {
		t.Fatal("empty db path should fail")
	}
	// 非法 URL：请求构造即失败，不触网。
	if _, err := SyncOnce(Options{MasterURL: "http://bad host", Token: "t", DBPath: path}, db, store); err == nil {
		t.Fatal("invalid url should fail")
	}
	// 解密成功但不是 sqlite 库：magic 校验拒收。
	enc, err := store.EncryptBytes([]byte("definitely not a sqlite db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SyncOnce(Options{MasterURL: newBodyServer(t, enc), Token: "t", DBPath: path}, db, store); err == nil || !strings.Contains(err.Error(), "not a sqlite db") {
		t.Fatalf("non-sqlite body err = %v", err)
	}
}

// TestSyncOnceSnapshotReadError 响应体截断：ReadAll 报错冒上，本机库不动。
func TestSyncOnceSnapshotReadError(t *testing.T) {
	store := secret.NewStore("standby-master")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("trunc"))
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				conn.Close()
			}
		}
	}))
	defer srv.Close()
	db, path := mkTarget(t)
	if _, err := SyncOnce(Options{MasterURL: srv.URL, Token: "t", DBPath: path}, db, store); err == nil || !strings.Contains(err.Error(), "read snapshot") {
		t.Fatalf("truncated snapshot err = %v", err)
	}
	var v string
	if err := db.Raw("SELECT v FROM mark").Scan(&v).Error; err != nil || v != "old" {
		t.Fatalf("old db unusable: %q %v", v, err)
	}
}

// TestSyncOnceTmpAndReplaceFail 落盘两处失败：tmp 目录缺失开文件失败、
// rename 目标被目录占住。
func TestSyncOnceTmpAndReplaceFail(t *testing.T) {
	store := secret.NewStore("standby-master")
	body := mkSource(t, store, "from-master")
	url := newBodyServer(t, body)

	db, _ := mkTarget(t)
	// tmp 父目录缺失：OpenFile 失败。
	if _, err := SyncOnce(Options{MasterURL: url, Token: "t", DBPath: "/no-such-dir-xyz/ferry.db"}, db, store); err == nil || !strings.Contains(err.Error(), "write tmp") {
		t.Fatalf("missing tmp dir err = %v", err)
	}
	// dbPath 是目录：rename 报错。
	if _, err := SyncOnce(Options{MasterURL: url, Token: "t", DBPath: t.TempDir()}, db, store); err == nil || !strings.Contains(err.Error(), "replace db") {
		t.Fatalf("dir target err = %v", err)
	}
}

// syncBuf 互斥保护的日志缓冲：Loop 协程写、测试协程读不构成竞争。
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestLoopRetriesAndStops 主面板不可达：按 tick 记错重试不放弃；ctx 取消即退。
func TestLoopRetriesAndStops(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()
	var out syncBuf
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Loop(ctx, Options{MasterURL: dead.URL, Token: "t", DBPath: t.TempDir() + "/ferry.db",
			Sec: 1, tick: 10 * time.Millisecond, Logger: log.New(&out, "", 0)},
			nil, nil, func() {})
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if strings.Contains(out.String(), "standby: sync:") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("loop never logged a failed sync; out = %q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop after cancel")
	}
}
