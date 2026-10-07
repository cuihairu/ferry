package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/cuihairu/ferry/server/internal/ringlog"
	"github.com/gin-gonic/gin"
)

// 运行日志查看（P1-11，来源：3x-ui 日志查看与 clear_logs_job；
// Marzban 节点侧日志由 agent 采集另行推进）。

// adminLogs 查看面板运行日志（GET /admin/logs）：环形缓冲最近 limit 条
// （时间升序，最新在后），limit 缺省 200、上限 1000。
func (h *Handler) adminLogs(c *gin.Context) {
	limit := 200
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-1000"))
			return
		}
		limit = n
	}
	c.JSON(http.StatusOK, gin.H{"entries": ringlog.Default().Entries(limit)})
}

// clearAdminLogs 清空运行日志（DELETE /admin/logs）。
func (h *Handler) clearAdminLogs(c *gin.Context) {
	ringlog.Default().Clear()
	c.JSON(http.StatusOK, gin.H{"cleared": true})
}
