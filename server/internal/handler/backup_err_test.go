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

// TestBackupDBSnapshotFail covers a 500 on archive production failure: the
// backup directory's parent path is a plain file, so MkdirAll reports
// not a directory.
func TestBackupDBSnapshotFail(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, db := newTestRouterCfg(t, func(c *config.Config) {
		c.BackupDir = filepath.Join(blocker, "sub")
	})
	seedAdminForJWT(t, db)
	if rec := doAdmin(t, r, http.MethodGet, "/admin/backup/db", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("snapshot fail: %d %s", rec.Code, rec.Body)
	}
}

// TestBackupDBRecordFail covers a 500 when the snapshot row fails to store (the archive itself was produced successfully).
func TestBackupDBRecordFail(t *testing.T) {
	r, db := newTestRouterCfg(t, func(c *config.Config) {
		c.BackupDir = t.TempDir()
	})
	seedAdminForJWT(t, db)
	db.Callback().Create().Before("gorm:create").Register("t:bak_create", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.Backup); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doAdmin(t, r, http.MethodGet, "/admin/backup/db", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("record fail: %d %s", rec.Code, rec.Body)
	}
}
