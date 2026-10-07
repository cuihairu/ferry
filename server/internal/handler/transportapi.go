package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/transport"
	"github.com/gin-gonic/gin"
)

// listTransportStatus 区域传输判定（GET /api/transport-status，E-17）：
// 区域内各传输形态的存活聚合与换线建议；window_sec 可调判定窗口（30-3600）。
func (h *Handler) listTransportStatus(c *gin.Context) {
	opts := transport.Options{}
	if v := c.Query("window_sec"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 30 || n > 3600 {
			fail(c, http.StatusBadRequest, errors.New("window_sec must be 30-3600"))
			return
		}
		opts.Window = time.Duration(n) * time.Second
	}
	rows, err := transport.Judge(h.db, opts)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}
