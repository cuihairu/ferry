// Package handler 装配 HTTP 路由与各资源的接口。
package handler

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler 持有共享依赖，各资源方法挂在其上，路由注册由 NewRouter 完成。
type Handler struct {
	db *sql.DB
}

// NewRouter 创建 gin 引擎并挂载全部路由。
func NewRouter(db *sql.DB) *gin.Engine {
	h := &Handler{db: db}
	r := gin.Default()

	r.GET("/api/health", h.health)

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
