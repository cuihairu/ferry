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

// Panel master/standby sync (P2-1) test: pull -> decrypt -> atomic replace full chain, four failure modes that do not replace:
// tamper/wrong key/wrong token/master unreachable, and the Loop semantics of the disabled state and the replace hook.

// closeDB closes the connection pool at the end.
func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// mkSource builds the source DB, writes one marker row, and returns the DB file bytes (encrypted or not per store).
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

// mkTarget builds the local DB (old data) to be replaced; returns the gorm handle and the file path.
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

// TestSyncOnceRoundtrip covers the full chain: pull the encrypted snapshot -> decrypt + validate -> replace the local DB,
// old data swapped for new, sidecar files left behind.
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
	// SyncOnce already silently closed the old connection pool; reopen to verify the persisted content.
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

// TestSyncOnceFailureModes covers four failure modes that do not replace the local DB: tamper (non-ciphertext),
// wrong master key, wrong token (403), master unreachable.
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
	// Wrong token runs separately (the srv 403 branch).
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
	// Master unreachable: the server with its port closed.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead.Close()
	db, path := mkTarget(t)
	if got, err := SyncOnce(Options{MasterURL: dead.URL, Token: "tok", DBPath: path}, db, store); err == nil || got {
		t.Fatalf("unreachable: replaced=%v err=%v", got, err)
	}
	// Wrong token: the 403 branch.
	db2, path2 := mkTarget(t)
	if got, err := SyncOnce(Options{MasterURL: srv.URL, Token: "bad", DBPath: path2}, db2, store); err == nil || got {
		t.Fatalf("wrong token: replaced=%v err=%v", got, err)
	}
	// Guards: empty URL / empty token / master key not configured.
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

// newBodyServer returns the address of a temporary server that always writes the body (cleaned up with the test directory).
func newBodyServer(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestLoopGuards covers Loop: MasterURL empty = disabled, returns immediately; a successful replacement triggers
// OnReplace exactly once and the loop exits.
func TestLoopGuards(t *testing.T) {
	// Disabled: returns directly (tick is extremely short to avoid a false wait).
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

// TestSQLitePath covers DSN path extraction: bare path / file: prefix / pragma query segment.
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

// TestDBPathFromDSNExport wraps export with the same behavior as sqlitePath (main takes the local DB path).
func TestDBPathFromDSNExport(t *testing.T) {
	if got := DBPathFromDSN("file:/data/ferry.db?_pragma=journal_mode(WAL)"); got != "/data/ferry.db" {
		t.Fatalf("DBPathFromDSN = %q", got)
	}
}

// TestSyncOnceGuardsAndBadSnap fills in the guards and bad-snapshot branches: empty dbPath, illegal URL,
// decryption succeeds but the file is not a sqlite DB (three more rejection cases beyond the wrong key).
func TestSyncOnceGuardsAndBadSnap(t *testing.T) {
	store := secret.NewStore("standby-master")
	db, path := mkTarget(t)

	// empty dbPath: nothing to replace.
	if _, err := SyncOnce(Options{MasterURL: "http://x", Token: "t", DBPath: ""}, db, store); err == nil {
		t.Fatal("empty db path should fail")
	}
	// Illegal URL: the request construction fails, never touching the network.
	if _, err := SyncOnce(Options{MasterURL: "http://bad host", Token: "t", DBPath: path}, db, store); err == nil {
		t.Fatal("invalid url should fail")
	}
	// Decryption succeeds but it is not a sqlite DB: the magic check rejects it.
	enc, err := store.EncryptBytes([]byte("definitely not a sqlite db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SyncOnce(Options{MasterURL: newBodyServer(t, enc), Token: "t", DBPath: path}, db, store); err == nil || !strings.Contains(err.Error(), "not a sqlite db") {
		t.Fatalf("non-sqlite body err = %v", err)
	}
}

// TestSyncOnceSnapshotReadError truncated response body: the ReadAll error bubbles up, the local DB is untouched.
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

// TestSyncOnceTmpAndReplaceFail covers two write failures: the tmp directory is missing so opening the file fails,
// and the rename target is occupied by a directory.
func TestSyncOnceTmpAndReplaceFail(t *testing.T) {
	store := secret.NewStore("standby-master")
	body := mkSource(t, store, "from-master")
	url := newBodyServer(t, body)

	db, _ := mkTarget(t)
	// tmp parent directory missing: OpenFile fails.
	if _, err := SyncOnce(Options{MasterURL: url, Token: "t", DBPath: "/no-such-dir-xyz/ferry.db"}, db, store); err == nil || !strings.Contains(err.Error(), "write tmp") {
		t.Fatalf("missing tmp dir err = %v", err)
	}
	// dbPath is a directory: rename errors.
	if _, err := SyncOnce(Options{MasterURL: url, Token: "t", DBPath: t.TempDir()}, db, store); err == nil || !strings.Contains(err.Error(), "replace db") {
		t.Fatalf("dir target err = %v", err)
	}
}

// syncBuf is a mutex-protected log buffer: the Loop goroutine writes and the test goroutine reads, not a data race.
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

// TestLoopRetriesAndStops master unreachable: logs errors per tick and retries without giving up; ctx cancellation exits.
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
