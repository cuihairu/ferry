// Package handler 装配 HTTP 路由与各资源的接口。
package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/ratelimit"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handler 持有共享依赖，各资源方法挂在其上，路由注册由 NewRouter 完成。
type Handler struct {
	db  *gorm.DB
	cfg config.Config
	hub *agenthub.Hub
	// redeemLimiter 是兑换接口的 IP 限流（PAY-5：10 次/分钟，失败 5 次锁 15 分钟）。
	redeemLimiter *ratelimit.Limiter
	// speedLimiter 是测速字节端点的 IP 限流（30 次/分钟，只 Allow 不记失败）。
	speedLimiter *ratelimit.Limiter
}

// NewRouter 创建 gin 引擎并挂载全部路由。
func NewRouter(db *gorm.DB, cfg config.Config) *gin.Engine {
	h := &Handler{db: db, cfg: cfg, hub: agenthub.New()}
	h.redeemLimiter = ratelimit.New(ratelimit.Options{
		Window:      time.Minute,
		MaxAttempts: 10,
		FailLimit:   5,
		Lockout:     15 * time.Minute,
	})
	h.speedLimiter = ratelimit.New(ratelimit.Options{
		Window:      time.Minute,
		MaxAttempts: 30,
	})
	r := gin.Default()

	r.GET("/api/health", h.health)
	r.GET("/agent/ws", h.agentWS)
	r.GET("/sub/:token", h.subscription)
	r.GET("/api/speedtest/bytes", h.speedtestBytes)

	api := r.Group("/api")
	{
		api.GET("/nodes", h.listNodes)
		api.POST("/nodes", h.createNode)
		api.GET("/nodes/:id", h.getNode)
		api.PUT("/nodes/:id", h.updateNode)
		api.DELETE("/nodes/:id", h.deleteNode)
		api.GET("/dimension-status", h.listDimensionStatus)
		api.GET("/probe-reports", h.listProbeReports)
		api.GET("/landings", h.listLandings)
		api.POST("/landings", h.createLanding)
		api.PUT("/landings/:id", h.updateLanding)
		api.DELETE("/landings/:id", h.releaseLanding)
		api.POST("/nodes/:id/config", h.pushConfig)
		api.GET("/nodes/:id/configs", h.listNodeConfigs)
		api.GET("/nodes/:id/traffic-logs", h.listNodeTraffic)
		api.GET("/nodes/:id/routing-config", h.renderRoutingConfig)
		api.POST("/nodes/:id/rulelib", h.pushRuleLib)
		api.GET("/nodes/:id/rulelib", h.listRuleLib)
		api.GET("/users", h.listUsers)
		api.POST("/users", h.createUser)
		api.GET("/users/:id", h.getUser)
		api.PUT("/users/:id", h.updateUser)
		api.DELETE("/users/:id", h.deleteUser)
		api.POST("/users/:id/sub-token", h.resetSubToken)
		api.GET("/users/:id/traffic", h.userTraffic)
		api.GET("/user-template", h.getUserTemplate)
		api.PUT("/user-template", h.putUserTemplate)
		api.POST("/traffic-logs", h.recordTraffic)
		api.POST("/card-batches", h.createCardBatch)
		api.GET("/card-batches", h.listCardBatches)
		api.GET("/card-batches/:id/codes", h.listCardCodes)
		api.PATCH("/card-codes/:id/disable", h.disableCardCode)
		api.DELETE("/card-batches/:id", h.deleteCardBatch)
		api.GET("/card-batches/:id/export.csv", h.exportCardBatchCSV)
		api.POST("/redeem", h.redeem)
		// 用户门户（PAY-7）：身份取自订阅令牌，见 panel.go。
		panel := api.Group("/panel")
		{
			panel.GET("/me", h.panelMe)
			panel.POST("/redeem", h.panelRedeem)
			panel.GET("/orders", h.panelOrders)
		}
	}
	// P1-1 管理员登录与鉴权
	api = r.Group("/api", apiAuthMiddleware())
	{
		api.GET("/token", h.GetCurrentUser)
		api.POST("/token", h.AdminGetApiToken)
	}
	admin := r.Group("/admin", adminAuthMiddleware())
	{
		admin.POST("/login", h.AdminLogin)
		admin.GET("/backup/db", h.backupDB)
		admin.GET("/web-cert", h.getWebCert)
		admin.PUT("/web-cert", h.putWebCert)
		admin.POST("/web-cert/selfsign", h.selfSignWebCert)
		admin.DELETE("/web-cert", h.deleteWebCert)
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
