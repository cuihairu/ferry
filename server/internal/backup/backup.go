// Package backup 本地周期备份（面板可用性 §2，P1）：SQLite VACUUM INTO
// 在线一致性快照（.backup 同义口径，WAL 已合并）→ 配置了主密钥时打包
// 加密（secret_store 派生钥 AES-256-GCM，主密钥不进备份包）→ 落档目录
// 滚动保留 Keep 份（含手动落档，超窗连文件带行删，不留幽灵 path）→
// backups 表落 scheduled 行。失败经 Herald backup_failed 告警（按日去重），
// 不静默。外发位 S3_* 只留配置面与 Uploader 接口位，本批不接真实外发。
// postgres/mysql 方言本批未支持（走各自备份设施），周期备份会按日告警。
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

// 备份留痕 kind 与档名前缀。
const (
	KindManual    = "manual"    // 手动下载端点落档
	KindScheduled = "scheduled" // 周期备份 Loop 落档
)

const (
	// TickSec 是调度检查周期（秒）：cron 触发粒度，实际触发延迟 ≤ 1 分钟。
	TickSec = 60
	// FreshHours 是 health 备份新鲜度阈值：距最近落档超过该时长视为不新鲜
	//（每日频次下容错过一次失败，两天没有新档就要看告警）。
	FreshHours = 48
)

// Uploader 是备份外发插件位（FERRY_BACKUP_S3_*，面板可用性 §2.2）：本批
// 仅预留接口未接真实外发。开启（S3Enabled）但未注入实现时，Loop 只在
// 日志明示「外发未接入 + 数据范围=备份目录全部落档文件」，档不离开本机。
type Uploader interface {
	// Upload 把本地档文件外发；成功返回 nil 后对应行置 uploaded=1。
	Upload(ctx context.Context, localPath string) error
}

// Options 是周期备份口径；零值回退默认（daily、保留 7 份）。
type Options struct {
	Dir       string   // 落档目录（FERRY_BACKUP_DIR）
	Cron      string   // 调度：cron 表达式或 "daily"/"HH:MM" 别名（FERRY_BACKUP_CRON）
	Keep      int      // 滚动保留份数（FERRY_BACKUP_KEEP）
	S3Enabled bool     // 外发位开关（FERRY_BACKUP_S3_ENABLED）
	S3        Uploader // 外发实现（本批恒 nil，插件位）
	Logger    *log.Logger

	tick  time.Duration // 调度检查周期（未导出，测试注入；缺省 TickSec）
	sched cron.Schedule // 调度注入（未导出，测试注入；缺省按 Cron 解析）
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

// dailyTimeRe 识别 "HH:MM" 别名（展开为每日该时刻的 cron 表达式）。
var dailyTimeRe = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)

// ParseSchedule 把 FERRY_BACKUP_CRON 配置解析为 cron 调度：空/"daily"=每日
// 午夜；"HH:MM"=每日该时刻；其余原样交给标准 5 段/@描述符解析，解析失败
// 回退每日并在日志说明（配置错不静默，也不让备份停摆）。
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

// Snapshot 生成一份当前数据库的一致性快照落档（SQLite VACUUM INTO 在线
// 备份，WAL 已合并）：配置了主密钥时打包加密为 .enc（secret 派生钥，
// 主密钥不进备份包），否则落明文 .db。返回档路径与字节数。
func Snapshot(db *gorm.DB, dir string, store *secret.Store) (string, int64, error) {
	if db.Dialector.Name() != "sqlite" {
		return "", 0, errors.New("backup only supports sqlite (postgres/mysql use their own facilities)")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", 0, fmt.Errorf("backup dir: %w", err)
	}
	// VACUUM INTO 要求目标文件不存在；CreateTemp 抢一个目录内安全名再删。
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
	raw, err := os.ReadFile(tmpPath)
	os.Remove(tmpPath)
	if err != nil {
		return "", 0, err
	}
	name := "ferry-backup-" + time.Now().Format("20060102-150405") + ".db"
	data := raw
	if store != nil && store.Enabled() {
		enc, encErr := store.EncryptBytes(raw)
		if encErr != nil {
			return "", 0, fmt.Errorf("encrypt backup: %w", encErr)
		}
		data = enc
		name += ".enc"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", 0, err
	}
	return path, int64(len(data)), nil
}

// Loop 周期备份调度：按 TickSec 轮询 cron 下个触发点，到点跑一轮
// （快照→落行→外发位→滚动清理），失败经 Herald backup_failed 告警。
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

// outboundState 是外发位的人类可读状态（启动日志与运行日志共用口径）。
func outboundState(opts Options) string {
	if !opts.S3Enabled {
		return "disabled"
	}
	if opts.S3 == nil {
		return "configured but NOT implemented (backups stay local; scope = all files in backup dir)"
	}
	return "enabled"
}

// run 跑一轮完整备份：快照落档 → backups 行 → 外发位（未实现只日志明示）
// → 滚动清理。
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
			if uerr := opts.S3.Upload(ctx, path); uerr != nil {
				opts.Logger.Printf("backup: s3 upload %s: %v", path, uerr)
			} else if uerr := db.Model(&storage.Backup{}).Where("id = ?", row.ID).
				Update("uploaded", true).Error; uerr != nil {
				opts.Logger.Printf("backup: mark uploaded: %v", uerr)
			}
		} else {
			opts.Logger.Printf("backup: FERRY_BACKUP_S3_ENABLED=1 but outbound not implemented; %s stays local (scope = all files in %s)", path, opts.Dir)
		}
	}
	Prune(db, opts.Keep)
	return nil
}

// Prune 滚动保留最近 keep 份档（manual/scheduled 不分），超窗的连文件带
// 行删；窗口内但文件已丢失的行同样清掉，不留幽灵 path。
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

// emitBackupFailed 落 backup_failed 告警（severity=warning，target=admin），
// 本地按日自查重（同日已有该 kind 事件则跳过——持续故障每日至多一条，
// 不刷屏），dedup_key 再兜底供 Herald 侧窗口合并；Emit 失败只记日志。
func emitBackupFailed(db *gorm.DB, cause error, logger *log.Logger) {
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ? AND created_at >= ?",
		herald.KindBackupFailed, time.Now().Truncate(24*time.Hour)).Count(&n).Error; err != nil {
		logger.Printf("backup: count backup_failed: %v", err)
	}
	if n > 0 {
		return
	}
	_, err := herald.Emit(db, herald.EmitInput{
		Kind: herald.KindBackupFailed, Severity: herald.SeverityWarning,
		Title:    "数据库备份失败",
		Body:     fmt.Sprintf("周期备份未完成：%v；连续失败请检查磁盘与数据库状态，备份链路故障不静默", cause),
		Target:   herald.TargetAdmin,
		DedupKey: "backup_failed:" + time.Now().Format("2006-01-02"),
		Meta:     map[string]any{"error": cause.Error()},
	})
	if err != nil {
		logger.Printf("backup: emit backup_failed: %v", err)
	}
}
