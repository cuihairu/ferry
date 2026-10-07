package handler

import (
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

// 备份导出（P1-6，对齐 3x-ui dump_sqlite 与 export 路由）。

// backupDB 导出 SQLite 数据库快照（GET /admin/backup/db）：
// VACUUM INTO 落到临时文件生成一致性快照（含 WAL 已合并内容），
// 以附件下载，请求结束即删；仅 SQLite 方言支持（postgres/mysql 走各自备份设施）。
func (h *Handler) backupDB(c *gin.Context) {
	if h.db.Dialector.Name() != "sqlite" {
		fail(c, http.StatusBadRequest, errors.New("backup download only supports sqlite"))
		return
	}
	tmp, err := os.CreateTemp("", "ferry-backup-*.db")
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	path := tmp.Name()
	tmp.Close()
	// VACUUM INTO 要求目标文件不存在，CreateTemp 只为抢占一个安全路径。
	if err := os.Remove(path); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	defer os.Remove(path)
	if err := h.db.Exec("VACUUM INTO ?", path).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	name := "ferry-backup-" + time.Now().Format("20060102-150405") + ".db"
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.File(path)
}
