package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// openDB builds a temporary SQLite DB (with all tables; backups/events participate in assertions).
func openDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestSnapshotManual covers snapshot archiving (no master key = plaintext .db): the archive opens
// independently, its table schema and data are intact, and the archived size matches.
func TestSnapshotManual(t *testing.T) {
	db := openDB(t)
	if err := db.Create(&storage.User{Username: "bk", QuotaBytes: 42}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	dir := t.TempDir()
	path, size, err := Snapshot(db, dir, secret.NewStore(""))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(path), "ferry-backup-") || strings.HasSuffix(path, ".enc") {
		t.Fatalf("snapshot name = %s, want plain ferry-backup-*.db", path)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != size {
		t.Fatalf("size mismatch: stat=%v size=%d", err, size)
	}
	sdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	var users []storage.User
	if err := sdb.Find(&users).Error; err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if len(users) != 1 || users[0].Username != "bk" || users[0].QuotaBytes != 42 {
		t.Fatalf("snapshot users mismatch: %+v", users)
	}
}

// TestSnapshotEncrypted covers a master-key-derived encrypted archive: a .enc archive cannot be opened
// as SQLite directly, and DecryptBytes restores it to something openable with the data visible (the master key never
// enters the backup archive, aligned with the "security design" §3 encrypted-surface policy).
func TestSnapshotEncrypted(t *testing.T) {
	db := openDB(t)
	if err := db.Create(&storage.User{Username: "enc", QuotaBytes: 7}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	store := secret.NewStore("test-master-key")
	path, _, err := Snapshot(db, t.TempDir(), store)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.HasSuffix(path, ".enc") {
		t.Fatalf("name = %s, want .enc suffix", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	plain, err := store.DecryptBytes(raw)
	if err != nil {
		t.Fatalf("decrypt backup: %v", err)
	}
	snap := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(snap, plain, 0o600); err != nil {
		t.Fatalf("write restored: %v", err)
	}
	sdb, err := gorm.Open(sqlite.Open(snap), &gorm.Config{})
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	var users []storage.User
	if err := sdb.Find(&users).Error; err != nil {
		t.Fatalf("query restored: %v", err)
	}
	if len(users) != 1 || users[0].Username != "enc" {
		t.Fatalf("restored users mismatch: %+v", users)
	}
}

// TestSnapshotUnsupportedDialect asserts a non-SQLite dialect errors outright (postgres/mysql
// use their own backup facilities, and periodic backup alerts via backup_failed instead of silently skipping).
func TestSnapshotUnsupportedDialect(t *testing.T) {
	pg, err := gorm.Open(postgres.Open("host=127.0.0.1 port=1 user=x dbname=x sslmode=disable"),
		&gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open pg dialector: %v", err)
	}
	if _, _, err := Snapshot(pg, t.TempDir(), nil); err == nil {
		t.Fatal("snapshot on postgres should fail")
	}
}

// TestPrune covers rolling retention: out-of-window archives are deleted file and row together; in-window
// rows whose files are already lost are cleared too (leaving no ghost paths). Nine real archives plus one ghost
// row with a lost file are preset, Keep=7: after sorting the ghost row takes the newest slot and is cleared, and the real archives keep the newest 6 (7-1), leaving 6 rows and 6 files.
func TestPrune(t *testing.T) {
	db := openDB(t)
	dir := t.TempDir()
	for i := 0; i < 9; i++ {
		p := filepath.Join(dir, strings.Repeat("x", i+1)+".db")
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if err := db.Create(&storage.Backup{Kind: KindScheduled, Path: p, SizeBytes: 4, CreatedAt: time.Now().Add(time.Duration(i) * time.Minute)}).Error; err != nil {
			t.Fatalf("seed row: %v", err)
		}
	}
	ghost := filepath.Join(dir, "gone.db")
	if err := db.Create(&storage.Backup{Kind: KindManual, Path: ghost, CreatedAt: time.Now().Add(30 * time.Minute)}).Error; err != nil {
		t.Fatalf("seed ghost: %v", err)
	}

	Prune(db, 7)

	var rows []storage.Backup
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("list rows: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want 6", len(rows))
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 6 {
		t.Fatalf("files = %d, want 6", len(files))
	}
	for _, r := range rows {
		if _, err := os.Stat(r.Path); err != nil {
			t.Fatalf("kept row %d path missing: %v", r.ID, err)
		}
	}
}

// TestParseSchedule covers the alias and fallback: daily means every day; HH:MM expands to that time;
// an invalid expression falls back to daily (a bad config is neither silent nor a stall).
func TestParseSchedule(t *testing.T) {
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, time.Local)
	daily := ParseSchedule("daily", nil)
	if got := daily.Next(now); got.Format("0102 15:04") != "1009 00:00" {
		t.Fatalf("daily next = %s, want next midnight", got)
	}
	at := ParseSchedule("03:30", nil)
	if got := at.Next(now); got.Format("0102 15:04") != "1009 03:30" {
		t.Fatalf("03:30 next = %s, want next-day 03:30 (past today's window)", got)
	}
	if got := at.Next(now.Add(4 * time.Hour)); got.Format("0102 15:04") != "1009 03:30" {
		t.Fatalf("03:30 after window = %s, want next-day 03:30", got)
	}
	fallback := ParseSchedule("not a cron", nil)
	if got := fallback.Next(now); got.Format("0102 15:04") != "1009 00:00" {
		t.Fatalf("fallback next = %s, want daily", got)
	}
	std := ParseSchedule("30 3 * * 0", nil) // standard 5 fields: Sundays at 03:30
	if got := std.Next(now); got.Format("0102 15:04") != "1011 03:30" {
		t.Fatalf("weekly next = %s, want Sunday 03:30", got)
	}
}

// TestBackupFailedEmit asserts a failure alert lands in the outbox (kind=backup_failed, deduped
// per day), consecutive failures yield only one on the same day, never silent.
func TestBackupFailedEmit(t *testing.T) {
	db := openDB(t)
	cause := errStub("vacuum into: disk full")
	emitBackupFailed(db, cause, nil)
	emitBackupFailed(db, cause, nil)
	var events []storage.Event
	if err := db.Where("kind = ?", "backup_failed").Find(&events).Error; err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (deduped per day)", len(events))
	}
	if events[0].Severity != "warning" || events[0].Target != "admin" {
		t.Fatalf("event = %+v, want warning/admin", events[0])
	}
	if !strings.Contains(events[0].Body, "disk full") {
		t.Fatalf("body = %q, want cause embedded", events[0].Body)
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }

// onceSchedule triggers once immediately and then stays silent for a long time (a deterministic schedule for the full-chain Loop test).
type onceSchedule struct{ fired *bool }

func (s onceSchedule) Next(t time.Time) time.Time {
	if !*s.fired {
		*s.fired = true
		return t
	}
	return t.Add(365 * 24 * time.Hour)
}

// TestLoopRunsOnceAndPrunes covers the full Loop chain (1s tick + an injected immediately-triggering
// schedule): when due it runs one round landing a scheduled row and an archive file, rolling cleanup takes
// effect, and a context cancel exits. Eight rows are preset to fill the window; after the new archive lands, it converges to 7 rows and 7 files by Keep=7.
func TestLoopRunsOnceAndPrunes(t *testing.T) {
	db := openDB(t)
	dir := t.TempDir()
	for i := 0; i < 8; i++ {
		p := filepath.Join(dir, strings.Repeat("y", i+1)+".db")
		if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if err := db.Create(&storage.Backup{Kind: KindScheduled, Path: p, CreatedAt: time.Now().Add(time.Duration(i) * time.Minute)}).Error; err != nil {
			t.Fatalf("seed row: %v", err)
		}
	}
	fired := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Loop(ctx, db, Options{Dir: dir, Keep: 7, tick: time.Second,
		sched: onceSchedule{fired: &fired}}, secret.NewStore("k"))

	deadline := time.Now().Add(10 * time.Second)
	for {
		var n int64
		db.Model(&storage.Backup{}).Count(&n)
		files, _ := os.ReadDir(dir)
		if n == 7 && len(files) == 7 {
			break // 8 old + 1 new = 9 -> converges to Keep=7
		}
		if time.Now().After(deadline) {
			t.Fatalf("loop did not converge: rows=%d files=%d", n, len(files))
		}
		time.Sleep(200 * time.Millisecond)
	}
	var scheduled storage.Backup
	db.Where("kind = ?", KindScheduled).Order("id DESC").First(&scheduled)
	if scheduled.Path == "" {
		t.Fatal("no scheduled row recorded")
	}
	if _, err := os.Stat(scheduled.Path); err != nil {
		t.Fatalf("new backup file missing: %v", err)
	}
}

// noopUploader is an always-successful upload stub: it exists only to hold up the "already injected" branch's state text.
type noopUploader struct{}

func (noopUploader) Upload(context.Context, string) error { return nil }

// TestOutboundState covers the three outbound-slot state texts: disabled, enabled-not-injected, enabled-injected.
func TestOutboundState(t *testing.T) {
	if got := outboundState(Options{}); got != "disabled" {
		t.Fatalf("disabled = %q", got)
	}
	if got := outboundState(Options{S3Enabled: true}); got != "enabled but uploader not injected (backups stay local; check FERRY_BACKUP_S3_*)" {
		t.Fatalf("uninjected = %q", got)
	}
	if got := outboundState(Options{S3Enabled: true, S3: noopUploader{}}); got != "enabled (s3-compatible endpoint; scope = all files in backup dir)" {
		t.Fatalf("injected = %q", got)
	}
}

// TestSnapshotLargeTableStreams covers the streaming path of a large-DB snapshot (P1-8): when the table
// size exceeds 1MiB (crossing the streaming buffer boundary) both the plaintext and encrypted archives restore fully.
// The old implementation read the whole DB into memory with ReadFile and EncryptBytes made another copy, so memory
// grew with the DB; streaming bounds memory, while the archive is still a consistent SQLite snapshot (VACUUM INTO online policy).
func TestSnapshotLargeTableStreams(t *testing.T) {
	db := openDB(t)
	if err := db.Exec(`CREATE TABLE big_tbl (id INTEGER PRIMARY KEY, payload BLOB)`).Error; err != nil {
		t.Fatal(err)
	}
	// 6000 rows x 256B = ~1.5MB (one insert via a recursive CTE).
	if err := db.Exec(`INSERT INTO big_tbl (payload)
		SELECT zeroblob(256) FROM (
			WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 6000)
			SELECT x FROM c)`).Error; err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	// Plaintext: the size crosses the 1MiB streaming buffer (proving the chunked path ran), the archive opens independently and the row count is complete.
	path, size, err := Snapshot(db, dir, secret.NewStore(""))
	if err != nil {
		t.Fatalf("plain snapshot: %v", err)
	}
	if size <= 1<<20 {
		t.Fatalf("snapshot size = %d, want >1MiB to exercise streaming", size)
	}
	assertBigRows := func(path string) {
		t.Helper()
		sdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		if err != nil {
			t.Fatalf("open snapshot: %v", err)
		}
		var n int64
		if err := sdb.Table("big_tbl").Count(&n).Error; err != nil {
			t.Fatalf("count snapshot: %v", err)
		}
		if n != 6000 {
			t.Fatalf("big_tbl rows = %d, want 6000", n)
		}
	}
	assertBigRows(path)

	// Encrypted: DecryptBytes (the unchanged restore policy) can decode the streamed output, with the row count complete after restore.
	store := secret.NewStore("stream-master")
	encPath, _, err := Snapshot(db, dir, store)
	if err != nil {
		t.Fatalf("encrypted snapshot: %v", err)
	}
	if !strings.HasSuffix(encPath, ".enc") {
		t.Fatalf("encrypted name = %s, want .enc", encPath)
	}
	raw, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatalf("read enc: %v", err)
	}
	plain, err := store.DecryptBytes(raw)
	if err != nil {
		t.Fatalf("decrypt streamed snapshot: %v", err)
	}
	restore := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(restore, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	assertBigRows(restore)
}
