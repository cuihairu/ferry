// Package handler 装配 HTTP 路由与各资源的接口。
package handler

import (
	"net/http"

	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/config"
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
	}
	return r
}

func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
