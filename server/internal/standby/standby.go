// Package standby panel master/standby sync (P2-1): the standby instance periodically pulls the master
// panel's encrypted snapshot (same format as backup.Snapshot: VACUUM INTO + master-key-derived key
// encryption, restorable directly by restore.sh); after the master key decrypts and validates it, atomically replace the local DB.
//
// Replacement semantics: first sql.DB.Close() silently drops this process's connections (in-flight requests recover on restart),
// then rename overwrites ferry.db and clears the old inode's -wal/-shm sidecar files (preventing a stale WAL from
// replaying into the new DB on restart and corrupting it), then it invokes OnReplace - main passes os.Exit(0) and lets
// systemd Restart=always bring it back up loading the new DB. RPO <= pull interval; when the master is unreachable
// or validation fails, do not replace and keep serving the stale DB (standby never silently drops availability).
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

// DefaultSec is the default pull interval (seconds): minute-level freshness is enough for price-page/config-tier data.
const DefaultSec = 600

// sqliteMagic is the SQLite file-header prefix: the decrypted product passes this check before being written,
// blocking three cases: truncation, wrong key, non-DB file (decryption succeeded but the content is corrupt).
var sqliteMagic = []byte("SQLite format 3\x00")

// Options is the sync policy; zero values fall back to defaults (interval DefaultSec).
type Options struct {
	MasterURL string // master panel address (FERRY_STANDBY_MASTER_URL, empty = disabled)
	Token     string // shared token (FERRY_STANDBY_TOKEN, both sides identical)
	Sec       int    // pull interval in seconds (FERRY_STANDBY_SEC)
	DBPath    string // local ferry.db path
	Logger    *log.Logger

	tick time.Duration // scheduling period (unexported, test injection; default Sec seconds)
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

// sqlitePath extracts the sqlite file path from the DSN: strips the file: prefix and cuts the ?pragma section.
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

// SyncOnce pulls one round: master panel snapshot -> master key decrypt + validate -> atomic replace of the local DB.
// returns replaced=true meaning the DB has been swapped (the caller should exit and hand off to a restart); any failure
// (master unreachable / token mismatch / decryption failure / non-DB file) does not replace and returns an error.
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

	// silently drop this process's connections before swapping files: after rename the old inode's -wal/-shm must be cleared,
	// otherwise on restart SQLite replays the stale WAL into the new DB.
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

// Loop pulls periodically until ctx is cancelled or one successful replacement (invokes OnReplace after the replace and
// returns - main passes os.Exit(0) and lets systemd restart to load the new DB, tests pass a noop).
// MasterURL empty = standby disabled, returns immediately.
func Loop(ctx context.Context, opts Options, db *gorm.DB, store *secret.Store, onReplace func()) {
	opts = opts.normalize()
	if opts.MasterURL == "" {
		return
	}
	// pull once on startup (standby cold-starts already aligned with the master), then periodically per interval.
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

// DBPathFromDSN lets main take the local DB path from the config (the DSN may carry file:/pragma).
func DBPathFromDSN(dsn string) string {
	return sqlitePath(dsn)
}
