// Package standby 面板主从同步（P2-1）：standby 实例周期拉取主面板的
// 加密快照（backup.Snapshot 同格式：VACUUM INTO + 主密钥派生钥加密，
// restore.sh 可直接恢复），主密钥解密校验后原子替换本机库。
//
// 替换语义：先 sql.DB.Close() 静默本进程连接（等待中的请求随重启恢复），
// 再 rename 覆盖 ferry.db 并清旧 inode 的 -wal/-shm 侧车文件（防重启误
// 回放陈旧 WAL 损坏新库），随后回调 OnReplace——main 传 os.Exit(0) 交
// systemd Restart=always 拉起加载新库。RPO ≤ 拉取间隔；主面板不可达或
// 校验失败时不替换，继续服务陈旧库（standby 不静默丢可用性）。
package standby

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/secret"
	"gorm.io/gorm"
)

// DefaultSec 是拉取间隔缺省值（秒）：价格页/配置级数据分钟级新鲜度足够。
const DefaultSec = 600

// sqliteMagic 是 SQLite 文件头前缀：解密产物先过这关再落盘，
// 挡截断/错钥/非库文件（解密成功但内容损坏）三类。
var sqliteMagic = []byte("SQLite format 3\x00")

// Options 是同步口径；零值回退默认（间隔 DefaultSec）。
type Options struct {
	MasterURL string        // 主面板地址（FERRY_STANDBY_MASTER_URL，空=不启动）
	Token     string        // 共享令牌（FERRY_STANDBY_TOKEN，双方一致）
	Sec       int           // 拉取间隔秒（FERRY_STANDBY_SEC）
	DBPath    string        // 本机 ferry.db 路径
	Logger    *log.Logger

	tick time.Duration // 调度周期（未导出，测试注入；缺省 Sec 秒）
}

func (o Options) normalize() Options {
	if o.Sec <= 0 {
		o.Sec = DefaultSec
	}
	if o.tick <= 0 {
		o.tick = time.Duration(o.Sec) * time.Second
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
	return o
}

// sqlitePath 从 DSN 取 sqlite 文件路径：剥 file: 前缀、截 ?pragma 段。
func sqlitePath(dsn string) string {
	p := dsn
	if strings.HasPrefix(p, "file:") {
		p = strings.TrimPrefix(p, "file:")
	}
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	return p
}

// SyncOnce 拉取一轮：主面板快照 → 主密钥解密校验 → 原子替换本机库。
// 返回 replaced=true 表示库已换新（调用方应退出交重启）；任何失败
// （主面板不可达/令牌不符/解密失败/非库文件）都不替换，返回错误。
func SyncOnce(opts Options, db *gorm.DB, store *secret.Store) (bool, error) {
	opts = opts.normalize()
	if opts.MasterURL == "" {
		return false, errors.New("standby: master url empty")
	}
	if opts.Token == "" {
		return false, errors.New("standby: token empty")
	}
	if store == nil || !store.Enabled() {
		return false, errors.New("standby: master key required to verify snapshot")
	}
	dbPath := opts.DBPath
	if dbPath == "" {
		return false, errors.New("standby: db path empty")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(opts.MasterURL, "/")+"/api/standby/snapshot", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("X-Standby-Token", opts.Token)
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("standby: fetch snapshot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("standby: master returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return false, fmt.Errorf("standby: read snapshot: %w", err)
	}
	raw, err := store.DecryptBytes(body)
	if err != nil {
		return false, fmt.Errorf("standby: decrypt snapshot: %w", err)
	}
	if len(raw) < len(sqliteMagic) || string(raw[:len(sqliteMagic)]) != string(sqliteMagic) {
		return false, errors.New("standby: snapshot is not a sqlite db")
	}

	tmp := dbPath + ".standby-tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return false, fmt.Errorf("standby: write tmp: %w", err)
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		os.Remove(tmp)
		return false, fmt.Errorf("standby: write tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return false, fmt.Errorf("standby: fsync tmp: %w", err)
	}
	f.Close()

	// 静默本进程连接再换文件：rename 后旧 inode 的 -wal/-shm 必须清掉，
	// 否则重启时 SQLite 会把陈旧 WAL 回放进新库。
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	if err := os.Rename(tmp, dbPath); err != nil {
		return false, fmt.Errorf("standby: replace db: %w", err)
	}
	for _, side := range []string{dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Remove(side); err != nil && !os.IsNotExist(err) {
			opts.Logger.Printf("standby: remove %s: %v", side, err)
		}
	}
	return true, nil
}

// Loop 周期拉取直到 ctx 取消或一次成功替换（替换后回调 OnReplace 并
// 返回——main 传 os.Exit(0) 交 systemd 重启加载新库，测试传 noop）。
// MasterURL 空 = standby 未开启，立即返回。
func Loop(ctx context.Context, opts Options, db *gorm.DB, store *secret.Store, onReplace func()) {
	opts = opts.normalize()
	if opts.MasterURL == "" {
		return
	}
	// 上线先拉一版（standby 冷启动即对齐主面板），之后按间隔周期。
	for {
		replaced, err := SyncOnce(opts, db, store)
		if err != nil {
			opts.Logger.Printf("standby: sync: %v", err)
		} else if replaced {
			opts.Logger.Printf("standby: db replaced from master, restarting to load")
			onReplace()
			return
		}
		timer := time.NewTimer(opts.tick)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// DBPathFromDSN 供 main 从配置取本机库路径（DSN 可能带 file:/pragma）。
func DBPathFromDSN(dsn string) string {
	return sqlitePath(dsn)
}
