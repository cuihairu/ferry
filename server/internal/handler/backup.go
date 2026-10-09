package handler

import (
	"net/http"
	"path/filepath"
	"time"

	"github.com/cuihairu/ferry/server/internal/backup"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 备份导出（P1-6 手动备份；P1 周期备份批起同面落档留痕）。

// backupDB 导出当前数据库快照（GET /admin/backup/db）：VACUUM INTO 在线
// 一致性快照落备份目录（配置了主密钥时为加密档 .enc），落 manual 行
// （uploaded 恒 0——manual 落档不走外发，外发仅周期 Loop 经 S3Uploader）
// 并按 KEEP 滚动清理后以附件下载该档；postgres/mysql 方言不支持（走各自
// 备份设施）。
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
