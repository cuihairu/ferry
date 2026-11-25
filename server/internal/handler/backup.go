package handler

import (
	"net/http"
	"path/filepath"
	"time"

	"github.com/cuihairu/ferry/server/internal/backup"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Backup export (P1-6 manual backup; from the P1 scheduled-backup batch the
// same surface also leaves a recorded archive).

// backupDB exports a snapshot of the current database (GET /admin/backup/db):
// a VACUUM INTO online consistency snapshot lands in the backup directory
// (an encrypted .enc archive when a master key is configured), a manual row
// is stored (uploaded is always 0 -- a manual archive is not uploaded; uploads
// only happen in the periodic Loop via S3Uploader), the archive is pruned
// against KEEP and then served as an attachment download; the postgres/mysql
// dialects are unsupported (they use their own backup facilities).
func (h *Handler) backupDB(c *gin.Context) {
	path, size, err := backup.Snapshot(h.db, h.cfg.BackupDir, h.secrets)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	row := storage.Backup{Kind: backup.KindManual, Path: path, SizeBytes: size, CreatedAt: time.Now()}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	backup.Prune(h.db, h.cfg.BackupKeep)
	name := filepath.Base(path)
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.File(path)
}
