// Package backup is local periodic backup (panel availability §2, P1): a SQLite VACUUM INTO
// online consistent snapshot (.backup under the same policy, WAL already merged) -> packed and
// encrypted when a master key is configured (secret_store derived key AES-256-GCM, the master key
// never enters the backup archive) -> archived with Keep rolling retention (including manual
// archives; out-of-window files and rows are both deleted, leaving no ghost paths) ->
// a scheduled row lands in the backups table. Failures alert via Herald backup_failed (deduped per day),
// never silent. The outbound slot S3_* is off by default with zero change; explicitly enabling it sends
// for real via S3Uploader (stdlib SigV4 PUT, see s3.go), success sets uploaded=1, failure alerts and keeps the archive.
package backup

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

// Backup trail kinds and archive name prefixes.
const (
	KindManual    = "manual"    // manual download endpoint archive
	KindScheduled = "scheduled" // periodic backup Loop archive
)

const (
	// TickSec is the schedule check period (seconds): cron trigger granularity, the actual trigger delay is at most 1 minute.
	TickSec = 60
	// FreshHours is the health backup-freshness threshold: newer than this long from the last archive counts as
	// fresh (at a daily cadence one skipped failure is tolerated; two days without a new archive means look at the alert).
	FreshHours = 48
)

// Uploader is the backup outbound plugin slot (FERRY_BACKUP_S3_*, panel availability §2.2): off by
// default; when explicitly enabled main injects S3Uploader (stdlib SigV4 PUT) to send for real, and
// tests can inject a stub. When the switch is on but no implementation is injected, Loop only states in the log "outbound not
// injected + data scope = all archived files under the backup directory"; the archive does not leave this machine.
type Uploader interface {
	// Upload sends the local archive file outbound; on nil return the corresponding row is set to uploaded=1.
	Upload(ctx context.Context, localPath string) error
}

// Options is the periodic backup policy; the zero value falls back to the default (daily, keep 7).
type Options struct {
	Dir       string   // archive directory (FERRY_BACKUP_DIR)
	Cron      string   // schedule: a cron expression or the "daily"/"HH:MM" alias (FERRY_BACKUP_CRON)
	Keep      int      // rolling retention count (FERRY_BACKUP_KEEP)
	S3Enabled bool     // outbound switch (FERRY_BACKUP_S3_ENABLED, off by default)
	S3        Uploader // outbound implementation (S3Uploader injected when enabled, a stub in tests)
	Logger    *log.Logger

	tick  time.Duration // schedule check period (unexported, injected by tests; defaults to TickSec)
	sched cron.Schedule // schedule injection (unexported, injected by tests; defaults to parsing Cron)
}

func (o Options) normalize() Options {
	if o.Dir == "" {
		o.Dir = "backups"
	}
	if o.Keep <= 0 {
		o.Keep = 7
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
	if o.tick <= 0 {
		o.tick = TickSec * time.Second
	}
	return o
}

// dailyTimeRe recognizes the "HH:MM" alias (expanded to a cron expression for that time daily).
var dailyTimeRe = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)

// ParseSchedule parses the FERRY_BACKUP_CRON setting into a cron schedule: empty / "daily" means daily
// midnight; "HH:MM" means that time daily; anything else is handed to the standard 5-field / @descriptor parser, and a
// parse failure falls back to daily with an explanation in the log (a bad config is neither silent nor does it stall the backup).
func ParseSchedule(expr string, logger *log.Logger) cron.Schedule {
	switch {
	case expr == "" || expr == "daily":
		expr = "@daily"
	case dailyTimeRe.MatchString(expr):
		var hh, mm int
		fmt.Sscanf(expr, "%d:%d", &hh, &mm)
		expr = fmt.Sprintf("%d %d * * *", mm, hh)
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		if logger != nil {
			logger.Printf("backup: invalid FERRY_BACKUP_CRON %q (%v), falling back to daily", expr, err)
		}
		sched, _ = cron.ParseStandard("@daily")
	}
	return sched
}

// Snapshot produces a consistent snapshot of the current database and archives it (SQLite VACUUM INTO online
// backup, WAL already merged): when a master key is configured it is streamed, packed and encrypted to .enc (secret derived
// key, the master key never enters the backup archive), otherwise it lands as plaintext .db. P1-8: no longer reading
// the whole DB into memory - plaintext takes an atomic rename in the same directory (zero copy), encryption takes the
// streaming EncryptBytesStream, so a large DB (growing traffic tables) does not OOM. Returns the archive path and byte count.
func Snapshot(db *gorm.DB, dir string, store *secret.Store) (string, int64, error) {
	if db.Dialector.Name() != "sqlite" {
		return "", 0, errors.New("backup only supports sqlite (postgres/mysql use their own facilities)")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", 0, fmt.Errorf("backup dir: %w", err)
	}
	// VACUUM INTO requires the target file not to exist; CreateTemp claims a safe name in the directory, then it is removed.
	tmp, err := os.CreateTemp(dir, "ferry-tmp-*.db")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	if err := os.Remove(tmpPath); err != nil {
		return "", 0, err
	}
	if err := db.Exec("VACUUM INTO ?", tmpPath).Error; err != nil {
		os.Remove(tmpPath)
		return "", 0, fmt.Errorf("vacuum into: %w", err)
	}
	name := "ferry-backup-" + time.Now().Format("20060102-150405") + ".db"
	if store != nil && store.Enabled() {
		name += ".enc"
	}
	path := filepath.Join(dir, name)
	// Plaintext: an atomic rename in the same directory, zero copy and no memory pressure (P1-8).
	if store == nil || !store.Enabled() {
		if err := os.Rename(tmpPath, path); err != nil {
			os.Remove(tmpPath)
			return "", 0, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", 0, err
		}
		return path, info.Size(), nil
	}
	// Encrypted: stream-encrypt the VACUUM output into the final archive (P1-8), clearing leftovers on failure.
	src, err := os.Open(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return "", 0, err
	}
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		src.Close()
		os.Remove(tmpPath)
		return "", 0, err
	}
	encErr := store.EncryptBytesStream(src, dst)
	dst.Close()
	src.Close()
	os.Remove(tmpPath)
	if encErr != nil {
		os.Remove(path)
		return "", 0, fmt.Errorf("encrypt backup: %w", encErr)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	return path, info.Size(), nil
}

// Loop is the periodic backup schedule: it polls the next cron trigger at TickSec and, when it
// comes, runs one round (snapshot -> row -> outbound slot -> rolling cleanup); failures alert via Herald backup_failed.
func Loop(ctx context.Context, db *gorm.DB, opts Options, store *secret.Store) {
	opts = opts.normalize()
	sched := opts.sched
	if sched == nil {
		sched = ParseSchedule(opts.Cron, opts.Logger)
	}
	opts.Logger.Printf("backup loop started: dir=%s keep=%d s3_enabled=%v (outbound uploader: %s)",
		opts.Dir, opts.Keep, opts.S3Enabled, outboundState(opts))
	ticker := time.NewTicker(opts.tick)
	defer ticker.Stop()
	next := sched.Next(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if now.Before(next) {
				continue
			}
			next = sched.Next(now)
			if err := run(ctx, db, opts, store); err != nil {
				opts.Logger.Printf("backup: %v", err)
				emitBackupFailed(db, err, opts.Logger)
			}
		}
	}
}

// outboundState is the human-readable outbound-slot state (shared by the startup and runtime logs).
func outboundState(opts Options) string {
	if !opts.S3Enabled {
		return "disabled"
	}
	if opts.S3 == nil {
		return "enabled but uploader not injected (backups stay local; check FERRY_BACKUP_S3_*)"
	}
	return "enabled (s3-compatible endpoint; scope = all files in backup dir)"
}

// run executes one full backup round: snapshot archive -> backups row -> outbound slot (S3 wired in,
// the 2026-10-09 outbound batch; no uploader configured means only the log states that nothing is sent) -> rolling cleanup.
func run(ctx context.Context, db *gorm.DB, opts Options, store *secret.Store) error {
	path, size, err := Snapshot(db, opts.Dir, store)
	if err != nil {
		return err
	}
	row := storage.Backup{Kind: KindScheduled, Path: path, SizeBytes: size, CreatedAt: time.Now()}
	if err := db.Create(&row).Error; err != nil {
		return fmt.Errorf("record backup row: %w", err)
	}
	if opts.S3Enabled {
		if opts.S3 != nil {
			// Each outbound log states the data scope (the fixed disclosure policy of the enabled state): what goes out
			// is every archived file under the backup directory (a full snapshot of the panel DB, an encrypted archive when a master key is configured).
			opts.Logger.Printf("backup: s3 outbound %s (data scope = all files in backup dir %s)", path, opts.Dir)
			if uerr := opts.S3.Upload(ctx, path); uerr != nil {
				opts.Logger.Printf("backup: s3 upload %s: %v", path, uerr)
				emitUploadFailed(db, uerr, opts.Logger)
			} else if uerr := db.Model(&storage.Backup{}).Where("id = ?", row.ID).
				Update("uploaded", true).Error; uerr != nil {
				opts.Logger.Printf("backup: mark uploaded: %v", uerr)
			} else {
				opts.Logger.Printf("backup: s3 upload %s done (uploaded=1)", path)
			}
		} else {
			opts.Logger.Printf("backup: FERRY_BACKUP_S3_ENABLED=1 but uploader not injected; %s stays local (check FERRY_BACKUP_S3_* config)", path)
		}
	}
	Prune(db, opts.Keep)
	return nil
}

// Prune keeps the keep most recent archives rolling (manual/scheduled alike), deleting out-of-window
// files and rows together; rows in-window whose files are already lost are cleared too, leaving no ghost paths.
func Prune(db *gorm.DB, keep int) {
	if keep <= 0 {
		keep = 7
	}
	var rows []storage.Backup
	if err := db.Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return
	}
	for i, r := range rows {
		stale := i >= keep
		if r.Path != "" {
			if _, err := os.Stat(r.Path); err != nil {
				stale = true
			}
		}
		if stale {
			if r.Path != "" {
				os.Remove(r.Path)
			}
			db.Delete(&storage.Backup{}, r.ID)
		}
	}
}

// emitBackupFailed lands a backup_failed alert for a snapshot failure (severity=warning,
// target=admin); the policy is documented in emitBackupEvent.
func emitBackupFailed(db *gorm.DB, cause error, logger *log.Logger) {
	emitBackupEvent(db, "数据库备份失败",
		fmt.Sprintf("周期备份未完成：%v；连续失败请检查磁盘与数据库状态，备份链路故障不静默", cause), logger)
}

// emitUploadFailed lands a backup_failed alert for an outbound failure: it shares the kind and the per-day
// dedup window with snapshot failures (already alerted today means only a log is added), and the local archive is kept by run.
func emitUploadFailed(db *gorm.DB, cause error, logger *log.Logger) {
	emitBackupEvent(db, "备份外发失败",
		fmt.Sprintf("备份档外发未完成（本地档已保留）：%v；请检查 FERRY_BACKUP_S3_* 配置与端点可达性", cause), logger)
}

// emitBackupEvent lands a backup_failed alert (severity=warning, target=admin), with a local per-day
// self-dedup (an existing event of that kind today is skipped - a continuing failure yields at most one a day,
// no spam), plus a dedup_key fallback for Herald-side window merging; an Emit failure only logs.
func emitBackupEvent(db *gorm.DB, title, body string, logger *log.Logger) {
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ? AND created_at >= ?",
		herald.KindBackupFailed, time.Now().Truncate(24*time.Hour)).Count(&n).Error; err != nil && logger != nil {
		logger.Printf("backup: count backup_failed: %v", err)
	}
	if n > 0 {
		return
	}
	_, err := herald.Emit(db, herald.EmitInput{
		Kind: herald.KindBackupFailed, Severity: herald.SeverityWarning,
		Title:    title,
		Body:     body,
		Target:   herald.TargetAdmin,
		DedupKey: "backup_failed:" + time.Now().Format("2006-01-02"),
		Meta:     map[string]any{"title": title, "body": body},
	})
	if err != nil && logger != nil {
		logger.Printf("backup: emit backup_failed: %v", err)
	}
}
