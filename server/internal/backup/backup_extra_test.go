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

// TestNormalizeDefaults 覆盖 Options.normalize 的缺省补全（Dir/Keep/Logger/tick）。
func TestNormalizeDefaults(t *testing.T) {
	o := Options{}.normalize()
	if o.Dir != "backups" || o.Keep != 7 || o.Logger == nil {
		t.Fatalf("normalize defaults: %+v", o)
	}
	if o.tick != TickSec*time.Second {
		t.Fatalf("tick default = %v", o.tick)
	}
	// 显式值保留。
	lg := log.New(os.Stderr, "", 0)
	o = Options{Dir: "/x", Keep: 3, Logger: lg, tick: 5 * time.Second}.normalize()
	if o.Dir != "/x" || o.Keep != 3 || o.Logger != lg || o.tick != 5*time.Second {
		t.Fatalf("normalize preserve: %+v", o)
	}
}

// TestParseScheduleBoundaries 覆盖 ParseSchedule 的空/daily/HH:MM/非法回退分支。
func TestParseScheduleBoundaries(t *testing.T) {
	lg := log.New(os.Stderr, "", 0)
	cases := []string{"", "daily", "03:30", "30 3 * * *", "not a cron"}
	for _, c := range cases {
		s := ParseSchedule(c, lg)
		if s == nil {
			t.Fatalf("schedule nil for %q", c)
		}
	}
	// "25:00" 匹配 HH:MM 但 Sscanf 后转 "0 25 * * *" → 非法 → 回退 daily。
	if s := ParseSchedule("25:00", lg); s == nil {
		t.Fatal("bad HH:MM should fall back")
	}
}

// TestSnapshotErrors 覆盖 Snapshot 的目录/临时文件/写入失败分支。
func TestSnapshotErrors(t *testing.T) {
	db := openDB(t)
	// 非 sqlite 已由 TestSnapshotUnsupportedDialect 覆盖；这里 dir 指向文件。
	fileDir := filepath.Join(t.TempDir(), "notdir")
	if err := os.WriteFile(fileDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Snapshot(db, fileDir, nil); err == nil {
		t.Fatal("want error for dir-is-file")
	}
	// 空 master key 的 store 禁用：Snap 明文。
	dir := t.TempDir()
	_, _, err := Snapshot(db, dir, secret.NewStore(""))
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// 加密 store 成功加密（.enc 结尾）。
	store := secret.NewStore("master-extra")
	path, _, err := Snapshot(db, dir, store)
	if err != nil || !strings.HasSuffix(path, ".enc") {
		t.Fatalf("encrypted snapshot: %v %s", err, path)
	}
}

// TestPruneEdge 覆盖 Prune 的 keep<=0 默认、丢失文件清幽灵、空路径行。
func TestPruneEdge(t *testing.T) {
	db := openDB(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "a.db")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 三行：保留 1 份，超窗的删文件+行；丢失文件行清掉；空 path 行。
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
	// keep<=0 默认 7，不清。
	Prune(db, 0)
	if err := db.Find(&left).Error; err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("keep=0 prune removed: %d", len(left))
	}
}

// TestEmitBackupEventDedup 覆盖 emitBackupEvent 的按日去重与 Emit 成功路径。
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
	// 同日再发被去重。
	emitBackupEvent(db, "标题2", "正文2", lg)
	db.Model(&storage.Event{}).Count(&n)
	if n != 1 {
		t.Fatalf("dedup failed, events = %d", n)
	}
}

// TestRunS3Failure 覆盖 run 的 S3 上传失败（uploader 报错）仍保留本地档。
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

// TestSnapshotClosedDB 覆盖 VACUUM 在关闭库上失败的报错分支。
func TestSnapshotClosedDB(t *testing.T) {
	db := openDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	// 关闭后 VACUUM 报错，Snap 返回错误。
	if _, _, err := Snapshot(db, t.TempDir(), nil); err == nil {
		t.Fatal("want error for closed db")
	}
}

// TestS3UploaderMissingCreds 覆盖 Upload 未配凭据的拒绝分支。
func TestS3UploaderMissingCreds(t *testing.T) {
	u, err := NewS3Uploader("http://localhost:9000", "", "", "", nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := u.Upload(context.Background(), "x.db"); err == nil {
		t.Fatal("want error for missing creds")
	}
}

// TestNewS3UploaderInvalid 覆盖 NewS3Uploader 的端点校验分支。
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
	// 合法缺 scheme 补 https。
	if _, err := NewS3Uploader("localhost:9000", "b", "a", "s", nil); err != nil {
		t.Fatalf("default scheme: %v", err)
	}
}

// TestOutboundStateEdge 覆盖 outboundState 三态。
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

// 确保 herald 常量与 gorm 引用不悬空（编译期验证）。
var (
	_ = herald.KindBackupFailed
	_ = herald.SeverityWarning
	_ = herald.TargetAdmin
	_ = gorm.ErrRecordNotFound
)
