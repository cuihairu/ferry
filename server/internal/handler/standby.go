package handler

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cuihairu/ferry/server/internal/backup"
	"github.com/gin-gonic/gin"
)

// Panel primary/standby sync (P2-1), primary-panel side: a machine-to-machine snapshot delivery endpoint.

// standbySnapshot serves the current database snapshot (GET /api/standby/snapshot): machine auth
// uses X-Standby-Token matched against FERRY_STANDBY_TOKEN (independent of the dash admin session,
// polled periodically by the standby instance); no token configured means the sync plane is off, always 403.
// Production reuses backup.Snapshot (VACUUM INTO + master-key encryption, directly restorable by
// restore.sh), written to a temp directory and deleted right after serving -- never recorded in the
// backups table and never taking a rolling retention slot.
func (h *Handler) standbySnapshot(c *gin.Context) {
	if h.cfg.StandbyToken == "" || c.GetHeader("X-Standby-Token") != h.cfg.StandbyToken {
		fail(c, http.StatusForbidden, errors.New("standby sync disabled or token mismatch"))
		return
	}
	if h.db.Dialector.Name() != "sqlite" {
		fail(c, http.StatusInternalServerError, errors.New("standby snapshot only supports sqlite"))
		return
	}
	dir, err := os.MkdirTemp("", "ferry-standby-")
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	defer os.RemoveAll(dir)
	path, _, err := backup.Snapshot(h.db, dir, h.secrets)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	name := filepath.Base(path)
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.File(path)
}
