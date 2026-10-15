package handler

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestBackupDBSnapshotFail 覆盖产档失败 500：备份目录父路径是普通文件，
// MkdirAll 报 not a directory。
func TestBackupDBSnapshotFail(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ := newTestRouterCfg(t, func(c *config.Config) {
		c.BackupDir = filepath.Join(blocker, "sub")
	})
	if rec := doAdmin(t, r, http.MethodGet, "/admin/backup/db", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("snapshot fail: %d %s", rec.Code, rec.Body)
	}
}

// TestBackupDBRecordFail 覆盖快照行落库失败 500（产档已成功）。
func TestBackupDBRecordFail(t *testing.T) {
	r, db := newTestRouterCfg(t, func(c *config.Config) {
		c.BackupDir = t.TempDir()
	})
	db.Callback().Create().Before("gorm:create").Register("t:bak_create", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.Backup); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doAdmin(t, r, http.MethodGet, "/admin/backup/db", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("record fail: %d %s", rec.Code, rec.Body)
	}
}
