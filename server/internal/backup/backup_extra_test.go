package backup

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestNormalizeDefaults covers the default completion of Options.normalize (Dir/Keep/Logger/tick).
func TestNormalizeDefaults(t *testing.T) {
	o := Options{}.normalize()
	if o.Dir != "backups" || o.Keep != 7 || o.Logger == nil {
		t.Fatalf("normalize defaults: %+v", o)
	}
	if o.tick != TickSec*time.Second {
		t.Fatalf("tick default = %v", o.tick)
	}
	// Explicit values are kept.
	lg := log.New(os.Stderr, "", 0)
	o = Options{Dir: "/x", Keep: 3, Logger: lg, tick: 5 * time.Second}.normalize()
	if o.Dir != "/x" || o.Keep != 3 || o.Logger != lg || o.tick != 5*time.Second {
		t.Fatalf("normalize preserve: %+v", o)
	}
}

// TestParseScheduleBoundaries covers ParseSchedule's empty / daily / HH:MM / invalid-fallback branches.
func TestParseScheduleBoundaries(t *testing.T) {
	lg := log.New(os.Stderr, "", 0)
	cases := []string{"", "daily", "03:30", "30 3 * * *", "not a cron"}
	for _, c := range cases {
		s := ParseSchedule(c, lg)
		if s == nil {
			t.Fatalf("schedule nil for %q", c)
		}
	}
	// "25:00" matches HH:MM but turns into "0 25 * * *" after Sscanf -> invalid -> falls back to daily.
	if s := ParseSchedule("25:00", lg); s == nil {
		t.Fatal("bad HH:MM should fall back")
	}
}

// TestSnapshotErrors covers Snapshot's directory / temp-file / write-failure branches.
func TestSnapshotErrors(t *testing.T) {
	db := openDB(t)
	// Non-sqlite is already covered by TestSnapshotUnsupportedDialect; here dir points at a file.
	fileDir := filepath.Join(t.TempDir(), "notdir")
	if err := os.WriteFile(fileDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Snapshot(db, fileDir, nil); err == nil {
		t.Fatal("want error for dir-is-file")
	}
	// A store with an empty master key is disabled: Snap goes plaintext.
	dir := t.TempDir()
	_, _, err := Snapshot(db, dir, secret.NewStore(""))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// An encrypted store encrypts successfully (.enc suffix).
	store := secret.NewStore("master-extra")
	path, _, err := Snapshot(db, dir, store)
	if err != nil || !strings.HasSuffix(path, ".enc") {
		t.Fatalf("encrypted snapshot: %v %s", err, path)
	}
}

// TestPruneEdge covers Prune's keep<=0 default, clearing ghosts of lost files, and empty-path rows.
func TestPruneEdge(t *testing.T) {
	db := openDB(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "a.db")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Three rows: keep 1 archive, delete file + row for the out-of-window one; clear the lost-file row; the empty-path row.
	rows := []storage.Backup{
		{Kind: KindScheduled, Path: p, CreatedAt: time.Now()},
		{Kind: KindScheduled, Path: filepath.Join(dir, "gone.db"), CreatedAt: time.Now().Add(-time.Hour)},
		{Kind: KindManual, Path: "", CreatedAt: time.Now().Add(-2 * time.Hour)},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	Prune(db, 1)
	var left []storage.Backup
	if err := db.Find(&left).Error; err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Path != p {
		t.Fatalf("prune left = %+v", left)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("kept file removed: %v", err)
	}
	// keep<=0 defaults to 7 and does not clear.
	Prune(db, 0)
	if err := db.Find(&left).Error; err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("keep=0 prune removed: %d", len(left))
	}
}

// TestEmitBackupEventDedup covers emitBackupEvent's per-day dedup and the Emit success path.
func TestEmitBackupEventDedup(t *testing.T) {
	db := openDB(t)
	lg := log.New(os.Stderr, "", 0)
	emitBackupEvent(db, "标题", "正文", lg)
	var n int64
	if err := db.Model(&storage.Event{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}
	// Sending again on the same day is deduped.
	emitBackupEvent(db, "标题2", "正文2", lg)
	db.Model(&storage.Event{}).Count(&n)
	if n != 1 {
		t.Fatalf("dedup failed, events = %d", n)
	}
}

// TestRunS3Failure covers run's S3 upload failure (the uploader errors) still keeping the local archive.
func TestRunS3Failure(t *testing.T) {
	db := openDB(t)
	dir := t.TempDir()
	opts := Options{Dir: dir, Keep: 7, Logger: log.New(os.Stderr, "", 0),
		S3Enabled: true, S3: failUploader{}, sched: fixedSchedule{}}
	if err := run(context.Background(), db, opts, secret.NewStore("")); err != nil {
		t.Fatalf("run: %v", err)
	}
	var n int64
	db.Model(&storage.Backup{}).Count(&n)
	if n != 1 {
		t.Fatalf("backup rows = %d, want 1 (local kept)", n)
	}
}

type failUploader struct{}

func (failUploader) Upload(context.Context, string) error { return os.ErrPermission }

type fixedSchedule struct{}

func (fixedSchedule) Next(time.Time) time.Time { return time.Now().Add(time.Minute) }

// TestSnapshotClosedDB covers the error branch where VACUUM fails on a closed DB.
func TestSnapshotClosedDB(t *testing.T) {
	db := openDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	// After closing, VACUUM errors and Snap returns an error.
	if _, _, err := Snapshot(db, t.TempDir(), nil); err == nil {
		t.Fatal("want error for closed db")
	}
}

// TestS3UploaderMissingCreds covers Upload's rejection branch when no credentials are configured.
func TestS3UploaderMissingCreds(t *testing.T) {
	u, err := NewS3Uploader("http://localhost:9000", "", "", "", nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := u.Upload(context.Background(), "x.db"); err == nil {
		t.Fatal("want error for missing creds")
	}
}

// TestNewS3UploaderInvalid covers NewS3Uploader's endpoint validation branches.
func TestNewS3UploaderInvalid(t *testing.T) {
	if _, err := NewS3Uploader("", "b", "a", "s", nil); err == nil {
		t.Fatal("empty endpoint")
	}
	if _, err := NewS3Uploader("://bad", "b", "a", "s", nil); err == nil {
		t.Fatal("parse fail")
	}
	if _, err := NewS3Uploader("http://", "b", "a", "s", nil); err == nil {
		t.Fatal("missing host")
	}
	// A legal endpoint missing its scheme gets https filled in.
	if _, err := NewS3Uploader("localhost:9000", "b", "a", "s", nil); err != nil {
		t.Fatalf("default scheme: %v", err)
	}
}

// TestOutboundStateEdge covers the three states of outboundState.
func TestOutboundStateEdge(t *testing.T) {
	if got := outboundState(Options{S3Enabled: false}); got != "disabled" {
		t.Fatalf("disabled: %s", got)
	}
	if got := outboundState(Options{S3Enabled: true}); !strings.Contains(got, "not injected") {
		t.Fatalf("not injected: %s", got)
	}
	if got := outboundState(Options{S3Enabled: true, S3: failUploader{}}); !strings.Contains(got, "s3-compatible") {
		t.Fatalf("enabled: %s", got)
	}
}

// Keeps the herald constants and gorm references from dangling (compile-time check).
var (
	_ = herald.KindBackupFailed
	_ = herald.SeverityWarning
	_ = herald.TargetAdmin
	_ = gorm.ErrRecordNotFound
)
