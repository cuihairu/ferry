package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/cuihairu/ferry/server/internal/ringlog"
	"github.com/gin-gonic/gin"
)

// Runtime log viewing (P1-11, origins: 3x-ui log viewing and clear_logs_job;
// Marzban node-side logs are collected by the agent and tracked separately).

// adminLogs shows panel runtime logs (GET /admin/logs): the most recent limit
// entries from the ring buffer (time ascending, newest last), with limit defaulting
// to 200 and capped at 1000.
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

// clearAdminLogs clears the runtime logs (DELETE /admin/logs).
func (h *Handler) clearAdminLogs(c *gin.Context) {
	ringlog.Default().Clear()
	c.JSON(http.StatusOK, gin.H{"cleared": true})
}
