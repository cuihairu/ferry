package handler

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cuihairu/ferry/server/internal/backup"
	"github.com/gin-gonic/gin"
)

// 面板主从同步（P2-1）主面板侧：机器对机器的快照下发端点。

// standbySnapshot 下发当前库快照（GET /api/standby/snapshot）：机器鉴权走
// X-Standby-Token 对齐 FERRY_STANDBY_TOKEN（与 dash 管理会话无关，供
// standby 实例周期拉取）；未配置 token = 同步面关闭，恒 403。产档复用
// backup.Snapshot（VACUUM INTO + 主密钥加密，restore.sh 可直接恢复），
// 落临时目录 serve 后即删——不进 backups 表、不占滚动保留位。
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
