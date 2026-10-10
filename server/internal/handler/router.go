// Package handler 装配 HTTP 路由与各资源的接口。
package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/backup"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/ratelimit"
	"github.com/cuihairu/ferry/server/internal/relaypush"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handler 持有共享依赖，各资源方法挂在其上，路由注册由 NewRouter 完成。
type Handler struct {
	db  *gorm.DB
	cfg config.Config
	hub *agenthub.Hub
	// pusher 是配置推送通道（手动下发与自动换线重指共用，E-16b）。
	pusher *relaypush.Pusher
	// secrets 是机密加密统一入口（R24）：云凭证等机密 AES-256-GCM 落库。
	secrets *secret.Store
	// redeemLimiter 是兑换接口的 IP 限流（PAY-5：10 次/分钟，失败 5 次锁 15 分钟）。
	redeemLimiter *ratelimit.Limiter
	// speedLimiter 是测速字节端点的 IP 限流（30 次/分钟，只 Allow 不记失败）。
	speedLimiter *ratelimit.Limiter
	// orderLimiter 是门户在线下单的 IP 限流（10 次/分钟，只 Allow 不记失败）。
	orderLimiter *ratelimit.Limiter
	// subLimiter 是订阅端点的 token 限流（安全设计 §4：按 token 限频防枚举，
	// 命中记日志；30 次/分钟，只 Allow 不记失败——只读端点无失败语义）。
	subLimiter *ratelimit.Limiter
	// subMissLimiter 按 IP 记订阅未命中（未知 token/停用用户）：窗口内 20 次
	// 未命中锁 15 分钟——单 token 限频拦不住每次换新 token 的枚举。
	subMissLimiter *ratelimit.Limiter
}

// NewRouter 创建 gin 引擎并挂载全部路由，同时交出配置推送器
// 供后台任务（alloc 自动换线）复用同一条下发链路。
func NewRouter(db *gorm.DB, cfg config.Config) (*gin.Engine, *relaypush.Pusher) {
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
	h.orderLimiter = ratelimit.New(ratelimit.Options{
		Window:      time.Minute,
		MaxAttempts: 10,
	})
	h.subLimiter = ratelimit.New(ratelimit.Options{
		Window:      time.Minute,
		MaxAttempts: 30,
	})
	// MaxAttempts 取大值：本限流器只用失败锁定语义，Allow 仅用来查锁定态。
	h.subMissLimiter = ratelimit.New(ratelimit.Options{
		Window:      time.Minute,
		MaxAttempts: 1 << 20,
		FailLimit:   20,
		Lockout:     15 * time.Minute,
	})
	h.pusher = relaypush.New(db, h.hub, nil)
	h.secrets = secret.NewStore(cfg.SecretKey)
	r := gin.Default()

	r.GET("/api/health", h.health)
	r.GET("/agent/ws", h.agentWS)
	r.GET("/sub/:token", h.subscription)
	r.GET("/feed.xml", h.feedXML)
	r.GET("/api/speedtest/bytes", h.speedtestBytes)

	// 管理数据面（2026-10-10 安全修复）：dash JWT 或 API Token 二选一，
	// 见 apitoken.go apiAuth。此前本组无任何中间件，管理接口公网裸奔。
	// AU-1：写操作经 auditMiddleware 统一落 audit_logs（GET 跳过）。
	api := r.Group("/api", h.apiAuth(), h.auditMiddleware())
	{
		api.GET("/nodes", h.listNodes)
		api.POST("/nodes", h.createNode)
		api.GET("/nodes/:id", h.getNode)
		api.PUT("/nodes/:id", h.updateNode)
		api.DELETE("/nodes/:id", h.deleteNode)
		api.GET("/dimension-status", h.listDimensionStatus)
		api.GET("/transport-status", h.listTransportStatus)
		api.GET("/probe-reports", h.listProbeReports)
		api.GET("/alerts", h.listAlerts)
		api.POST("/alerts/:id/resolve", h.resolveAlert)
		api.GET("/landings", h.listLandings)
		api.POST("/landings", h.createLanding)
		api.PUT("/landings/:id", h.updateLanding)
		api.DELETE("/landings/:id", h.releaseLanding)
		api.GET("/pool", h.listPool)
		api.POST("/pool/:id/suspend", h.suspendPoolNode)
		api.POST("/pool/:id/resume", h.resumePoolNode)
		api.POST("/pool/:id/standby", h.standbyPoolNode)
		api.GET("/load", h.listLoad)
		api.GET("/providers", h.listProviders)
		api.POST("/providers", h.createProvider)
		api.PUT("/providers/:id", h.updateProvider)
		api.DELETE("/providers/:id", h.deleteProvider)
		api.POST("/provision-templates/:id/apply", h.applyTemplate)
		api.POST("/provision-templates/:id/plan", h.planTemplate)
		api.GET("/provision-jobs", h.listProvisionJobs)
		api.GET("/recoveries", h.listRecoveries)
		api.GET("/recoveries/:id/actions", h.listRecoveryActions)
		// 域名前置（BR-2）：DNS 商凭证与前置记录。
		api.GET("/dns-providers", h.listDNSProviders)
		api.POST("/dns-providers", h.createDNSProvider)
		api.PUT("/dns-providers/:id", h.updateDNSProvider)
		api.DELETE("/dns-providers/:id", h.deleteDNSProvider)
		api.GET("/dns-fronts", h.listDNSFronts)
		api.POST("/dns-fronts", h.createDNSFront)
		api.PUT("/dns-fronts/:id", h.updateDNSFront)
		api.DELETE("/dns-fronts/:id", h.deleteDNSFront)
		// 分地域对账补偿入口（E-27）：事件驱动同步失败/人工改库后手动对齐。
		api.POST("/dns-fronts/geo-sync", h.geoSync)
		// 证书任务（BR-4）：编排与手动触发。
		api.GET("/cert-tasks", h.listCertTasks)
		api.POST("/cert-tasks", h.createCertTask)
		api.PUT("/cert-tasks/:id", h.updateCertTask)
		api.DELETE("/cert-tasks/:id", h.deleteCertTask)
		api.POST("/cert-tasks/:id/issue", h.issueCertTask)
		api.GET("/provision-templates", h.listTemplates)
		api.POST("/provision-templates", h.createTemplate)
		api.PUT("/provision-templates/:id", h.updateTemplate)
		api.DELETE("/provision-templates/:id", h.deleteTemplate)
		api.GET("/alloc", h.listAlloc)
		api.PUT("/alloc/policy", h.putAllocPolicy)
		api.GET("/quota-link", h.listQuotaLink)
		api.PUT("/quota-link", h.putQuotaLink)
		// 事件 outbox（HERALD-1）：dash 可见 pending/failed，死信人工重投。
		api.GET("/events", h.listEvents)
		api.POST("/events/:id/retry", h.retryEvent)
		// 通道投递看板与自检（HC-1/2/3）：回执聚合+健康徽标、自检测试事件、
		// 各类告警样例预览。
		api.GET("/events/deliveries", h.listEventDeliveries)
		api.GET("/events/samples", h.listEventSamples)
		api.POST("/events/test", h.testEvent)
		api.GET("/cost", h.getCost)
		api.GET("/evening", h.getEvening)
		api.PUT("/cost/threshold", h.putCostThreshold)
		// 成本参考库（E-31）：价格表导入/试查/批量偏差（参考价只提示不改价）。
		api.GET("/cost/ref", h.getCostRef)
		api.GET("/cost/ref-check", h.getCostRefCheck)
		api.PUT("/cost/ref-table", h.putCostRefTable)
		// 价格关注（E-32）：条件 CRUD 与快照（扫描告警在 cost.WatchLoop）。
		api.GET("/cost/watches", h.listWatches)
		api.POST("/cost/watches", h.createWatch)
		api.PUT("/cost/watches/:id", h.updateWatch)
		api.DELETE("/cost/watches/:id", h.deleteWatch)
		api.GET("/cost/watches/:id/snapshots", h.listWatchSnapshots)
		api.POST("/nodes/:id/config", h.pushConfig)
		api.POST("/nodes/:id/proc", h.nodeProcOp)
		api.POST("/nodes/:id/upgrade", h.nodeUpgrade)
		api.POST("/nodes/batch/proc", h.batchNodeProc)
		api.POST("/nodes/batch/config", h.batchNodeConfig)
		api.GET("/nodes/:id/configs", h.listNodeConfigs)
		api.GET("/nodes/:id/traffic-logs", h.listNodeTraffic)
		api.GET("/nodes/:id/routing-config", h.renderRoutingConfig)
		api.PUT("/routing/ads", h.putAdsEnabled)
		api.GET("/save-stats", h.saveStats)

		// 入口域名数据面（TOUCH-3）：域名例行邮件与断联容灾的共同数据源。
		api.GET("/entry-domains", h.listEntryDomains)
		api.POST("/entry-domains", h.createEntryDomain)
		api.PUT("/entry-domains/:id", h.updateEntryDomain)
		api.DELETE("/entry-domains/:id", h.deleteEntryDomain)
		// 断联态标记（TOUCH-7）：管理员手动开关，订阅注释随之加警告行。
		api.GET("/outage", h.getOutage)
		api.PUT("/outage", h.putOutage)
		api.GET("/nodes/:id/share", h.nodeShare)
		api.GET("/nodes/:id/logs", h.nodeProcLogs)
		api.GET("/nodes/:id/procs", h.nodeProcs)
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
		// 分销代理（DS-1）：建号/停用/佣金比例与账目流水，payout 随 DS-2。
		api.GET("/distributors", h.listDistributors)
		api.POST("/distributors", h.createDistributor)
		api.PUT("/distributors/:id", h.updateDistributor)
		api.GET("/distributors/:id/ledger", h.distributorLedger)
		// 结算打款与人工调（DS-2）：手动落账，不自动打款。
		api.POST("/distributors/:id/payout", h.distributorPayout)
		api.POST("/distributors/:id/adjust", h.distributorAdjust)
		// 在线支付回调（PAY-8）：挂 open 组（自带 Provider 验签），见文件尾。
		api.GET("/payments/reconcile", h.listReconcile)
		// 订单详情与退款流转（OD-2）
		api.GET("/payments/orders/:order_no", h.adminOrderDetail)
		api.POST("/payments/orders/:order_no/refund", h.refundOrder)
		// 站内信通知中心（NT-1）：dash 公告扇出与全量列表。
		api.GET("/notifications", h.listNotifications)
		api.POST("/notifications/announcement", h.createAnnouncement)
		api.DELETE("/notifications/:id", h.deleteNotification)
		// P1-1 管理员登录与鉴权（API Token 签发走 apiAuth 双凭据口径）。
		api.GET("/token", h.GetCurrentUser)
		api.POST("/token", h.AdminGetApiToken)
		// 操作审计（AU-1）：写操作流水查询，admin 写操作经 auditMiddleware 落行。
		api.GET("/audit-logs", h.ListAuditLogs)
		// 优惠码（PROMO-1）：管理面 CRUD；核销在 panel 下单（panel.go）。
		api.GET("/coupons", h.listCoupons)
		api.POST("/coupons", h.createCoupon)
		api.PUT("/coupons/:id", h.updateCoupon)
		api.DELETE("/coupons/:id", h.deleteCoupon)
		// 限时活动（PROMO-2）：管理面 CRUD；自动适用在 panel 下单（取优）。
		api.GET("/campaigns", h.listCampaigns)
		api.POST("/campaigns", h.createCampaign)
		api.PUT("/campaigns/:id", h.updateCampaign)
		api.DELETE("/campaigns/:id", h.deleteCampaign)
	}
	// 自带凭据/验签的公开面：panel 按订阅令牌（panel.go）、bot 按服务令牌
	//（TOUCH-6）、Herald 回执与支付回调各自验签——不经 apiAuth，故挂独立组。
	open := r.Group("/api")
	{
		panel := open.Group("/panel")
		{
			panel.GET("/me", h.panelMe)
			panel.GET("/contact", h.panelContact)
			panel.PUT("/contact", h.panelContactPut)
			panel.GET("/savings", h.panelSavings)
			panel.POST("/redeem", h.panelRedeem)
			panel.GET("/orders", h.panelOrders)
			// 在线下单（PAY-11，对 epusdt 段）
			panel.GET("/products", h.panelProducts)
			panel.GET("/campaigns", h.panelCampaigns)
			panel.POST("/orders", h.panelCreateOrder)
			panel.GET("/orders/:order_no", h.panelOrderStatus)
			// 通知中心（NT-1）与偏好（NT-2）
			panel.GET("/notifications", h.panelNotifications)
			panel.GET("/notifications/unread-count", h.panelUnreadCount)
			panel.POST("/notifications/read-all", h.panelMarkAllRead)
			panel.POST("/notifications/:id/read", h.panelMarkRead)
			panel.GET("/notify-prefs", h.panelNotifyPrefs)
			panel.PUT("/notify-prefs", h.panelUpdateNotifyPrefs)
			// 客服嵌入配置（servify 真嵌验收）：未接客服时 enabled=false。
			panel.GET("/support", h.panelSupport)
		}
		// TG bot 对接面（TOUCH-6）：服务级令牌鉴权，bot 后端独立部署
		// （不依赖面板域名存活），按用户 tg_chat_id 定位（TOUCH-1 绑定）。
		bot := open.Group("/bot")
		{
			bot.GET("/summary", h.botSummary)
		}
		// Herald 异步回投的通道分发回执（HERALD-2）：自带 HMAC 验签。
		open.POST("/internal/event-results", h.eventResult)
		// 在线支付回调（PAY-8）：自带 Provider 验签。
		open.POST("/pay/epusdt/notify", h.epusdtNotify)
	}
	// P1-1 管理员登录是获取首个令牌的唯一入口，公开挂在鉴权组外
	//（此前误挂组内导致登录面 401 不可达，安全批修复）。
	r.POST("/admin/login", h.AdminLogin)
	// DS-1 代理登录：独立端点公开挂载，role=distributor 令牌与管理员互不越界。
	r.POST("/distributor/login", h.DistributorLogin)
	// DS-3 代理自面：只读视图（自有批次/卡密/客户/订单/结算）。
	dist := r.Group("/distributor/api", h.distAuthMiddleware())
	{
		dist.GET("/me", h.distMe)
		dist.GET("/batches", h.distBatches)
		dist.GET("/batches/:id/codes", h.distBatchCodes)
		dist.GET("/customers", h.distCustomers)
		dist.GET("/orders", h.distOrders)
		dist.GET("/ledger", h.distLedger)
	}
	// AU-1：管理面写操作同口径落审计（GET 跳过）。
	admin := r.Group("/admin", h.adminAuthMiddleware(), h.auditMiddleware())
	{
		admin.GET("/backup/db", h.backupDB)
		admin.GET("/web-cert", h.getWebCert)
		admin.PUT("/web-cert", h.putWebCert)
		admin.POST("/web-cert/selfsign", h.selfSignWebCert)
		admin.DELETE("/web-cert", h.deleteWebCert)
		admin.GET("/status", h.sysStatus)
		admin.GET("/notify", h.getNotify)
		admin.PUT("/notify", h.putNotify)
		admin.POST("/notify/test", h.testNotify)
		admin.GET("/logs", h.adminLogs)
		admin.DELETE("/logs", h.clearAdminLogs)
		// 两步验证与登录审计（安全设计 §1，P1）
		admin.GET("/2fa", h.twofaStatus)
		admin.POST("/2fa/setup", h.twofaSetup)
		admin.POST("/2fa/enable", h.twofaEnable)
		admin.POST("/2fa/disable", h.twofaDisable)
		admin.GET("/login-logs", h.loginLogs)
	}
	return r, h.pusher
}

// errProbeRollback 是 DB 可写探针的回滚哨兵：事务内 INSERT 成功即证明
// 可写，随后回滚不留行；探针真实失败（只读盘/连接断）才计不可写。
var errProbeRollback = errors.New("health probe rollback")

// health 健康自检（面板可用性 §5，P1）：DB 可写、agent 在线数、备份
// 新鲜度（距最近落档时长，超 FreshHours 视为不新鲜）。检查项异常不静默：
// status 置 degraded 如实上报（仍 200，探活方不误杀）。
func (h *Handler) health(c *gin.Context) {
	status := "ok"
	dbWritable := true
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&storage.Setting{Key: "_health_probe", Value: "1"}).Error; err != nil {
			return err
		}
		return errProbeRollback
	}); err != nil && !errors.Is(err, errProbeRollback) {
		dbWritable = false
		status = "degraded"
	}

	backupView := gin.H{"last_at": nil, "age_hours": nil, "fresh": false}
	var last storage.Backup
	switch err := h.db.Order("created_at DESC, id DESC").First(&last).Error; {
	case err == nil:
		age := time.Since(last.CreatedAt).Hours()
		backupView["last_at"] = last.CreatedAt
		backupView["age_hours"] = age
		backupView["fresh"] = age <= backup.FreshHours
		backupView["last_kind"] = last.Kind
		if age > backup.FreshHours {
			status = "degraded"
		}
	case errors.Is(err, gorm.ErrRecordNotFound):
		status = "degraded" // 从未备份过
	}

	c.JSON(http.StatusOK, gin.H{
		"status": status,
		"checks": gin.H{
			"db_writable":   dbWritable,
			"agents_online": h.hub.OnlineCount(),
			"backup":        backupView,
		},
	})
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
