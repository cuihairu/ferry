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

// openDB 建临时 SQLite 库（含全部表，backups/events 参与断言）。
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

// TestSnapshotManual 覆盖快照落档（无主密钥=明文 .db）：档可独立打开、
// 表结构与数据完整、落档体积一致。
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

// TestSnapshotEncrypted 覆盖主密钥派生钥加密档：.enc 档不可直接当
// SQLite 打开，DecryptBytes 还原后可打开查到数据（主密钥不进备份包，
// 对齐《安全设计》§3 加密面口径）。
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

// TestSnapshotUnsupportedDialect 断言非 SQLite 方言直接报错（postgres/mysql
// 走各自备份设施，周期备份会按 backup_failed 告警而非静默跳过）。
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

// TestPrune 覆盖滚动保留：超窗档连文件带行删；窗口内文件已丢的行也清
// （不留幽灵 path）。预置 9 个真实档 + 1 条文件已丢的幽灵行，Keep=7：
// 排序后幽灵行占最新一位被清，真实档保留最新 6 个（7-1），共 6 行 6 文件。
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

// TestParseSchedule 覆盖别名与回退：daily=每日；HH:MM 展开为该时刻；
// 非法表达式回退每日（配置错不静默也不停摆）。
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
	std := ParseSchedule("30 3 * * 0", nil) // 标准 5 段：每周日 03:30
	if got := std.Next(now); got.Format("0102 15:04") != "1011 03:30" {
		t.Fatalf("weekly next = %s, want Sunday 03:30", got)
	}
}

// TestBackupFailedEmit 断言失败告警落 outbox（kind=backup_failed，按日
// dedup），连续失败同日只一条，不静默。
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

// onceSchedule 立即触发一次后长期沉默（Loop 全链路测试的确定性调度）。
type onceSchedule struct{ fired *bool }

func (s onceSchedule) Next(t time.Time) time.Time {
	if !*s.fired {
		*s.fired = true
		return t
	}
	return t.Add(365 * 24 * time.Hour)
}

// TestLoopRunsOnceAndPrunes 覆盖 Loop 全链路（1s tick + 注入立即触发的
// 调度）：到点跑一轮落 scheduled 行与档文件，滚动清理生效，context 取消
// 即退出。预置 8 行占满窗口，新档落地后按 Keep=7 收敛为 7 行 7 文件。
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
			break // 8 旧 + 1 新 = 9 → Keep=7 收敛
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
