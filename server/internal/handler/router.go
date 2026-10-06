// Package handler 装配 HTTP 路由与各资源的接口。
package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handler 持有共享依赖，各资源方法挂在其上，路由注册由 NewRouter 完成。
type Handler struct {
	db  *gorm.DB
	cfg config.Config
	hub *agenthub.Hub
}

// NewRouter 创建 gin 引擎并挂载全部路由。
func NewRouter(db *gorm.DB, cfg config.Config) *gin.Engine {
	h := &Handler{db: db, cfg: cfg, hub: agenthub.New()}
	r := gin.Default()

	r.GET("/api/health", h.health)
	r.GET("/agent/ws", h.agentWS)

	api := r.Group("/api")
	{
		api.GET("/nodes", h.listNodes)
		api.POST("/nodes", h.createNode)
		api.GET("/nodes/:id", h.getNode)
		api.PUT("/nodes/:id", h.updateNode)
		api.DELETE("/nodes/:id", h.deleteNode)
		api.GET("/dimension-status", h.listDimensionStatus)
		api.GET("/probe-reports", h.listProbeReports)
		api.POST("/nodes/:id/config", h.pushConfig)
		api.GET("/nodes/:id/configs", h.listNodeConfigs)
		api.GET("/nodes/:id/traffic-logs", h.listNodeTraffic)
	}
	return r
}

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// listDimensionStatus 返回区域/运营商维度的状态灯数据。
func (h *Handler) listDimensionStatus(c *gin.Context) {
	out := []storage.DimensionStatus{}
	if err := h.db.Order("scope, key").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// listProbeReports 返回边缘探测结论存证（新结论在前，默认 200 条）。
func (h *Handler) listProbeReports(c *gin.Context) {
	limit := 200
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-1000"))
			return
		}
		limit = n
	}
	out := []storage.ProbeReport{}
	if err := h.db.Order("probed_at DESC, id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
