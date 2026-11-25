// Package handler assembles the HTTP routes and per-resource endpoints.
package handler

import (
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/backup"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/idp"
	"github.com/cuihairu/ferry/server/internal/ratelimit"
	"github.com/cuihairu/ferry/server/internal/relaypush"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handler holds the shared dependencies; the per-resource methods hang off it,
// and route registration is done by NewRouter.
type Handler struct {
	db  *gorm.DB
	cfg config.Config
	hub *agenthub.Hub
	// pusher is the config-push channel (shared by manual pushes and automatic
	// relay re-pointing, E-16b).
	pusher *relaypush.Pusher
	// secrets is the unified secret-encryption entry point (R24): secrets such
	// as cloud credentials are stored AES-256-GCM encrypted.
	secrets *secret.Store
	// redeemLimiter is the IP rate limit for the redeem endpoint (PAY-5:
	// 10/min, 5 failures lock for 15 minutes).
	redeemLimiter *ratelimit.Limiter
	// loginLimiter is the IP rate limit for the credential login surface
	// (P2: admin/distributor login, 10/min, 5 failures lock for 15 minutes,
	// same style as redeem). Kept separate from redeemLimiter: login brute-force
	// and redemption no longer lock each other out (they previously shared
	// redeemLimiter, so hammering either surface caused a cross-surface DoS).
	loginLimiter *ratelimit.Limiter
	// speedLimiter is the IP rate limit for the speedtest byte endpoint
	// (30/min, Allow only, no failure counting).
	speedLimiter *ratelimit.Limiter
	// orderLimiter is the IP rate limit for portal online ordering
	// (10/min, Allow only, no failure counting).
	orderLimiter *ratelimit.Limiter
	// subLimiter is the token rate limit for the subscription endpoint
	// (security design §4: per-token throttling against enumeration, hits are
	// logged; 30/min, Allow only, no failure counting -- a read-only endpoint
	// has no failure semantics).
	subLimiter *ratelimit.Limiter
	// subMissLimiter records subscription misses per IP (unknown token /
	// disabled user): 20 misses within the window lock for 15 minutes -- a
	// single-token rate limit cannot stop enumeration that rotates tokens.
	subMissLimiter *ratelimit.Limiter
	// enrollLimiter is the IP rate limit for the node enrollment exchange
	// (FERRY-001: 10/min, 5 failures lock for 15 minutes, same style as
	// redeem -- blocks online brute-forcing of credentials).
	enrollLimiter *ratelimit.Limiter
	// dedupe is the idempotency window for report messages (FERRY-004,
	// bucketed by node_id; semantics in `docs/design/agent架构设计.md` §8 and
	// agentws_dedupe.go).
	dedupe sync.Map // nodeID -> *reportDeduper
	// oidc is the external identity provider client (G7). It is nil when
	// disabled, and the surface 404s first, so the nil state is unreachable.
	oidc *idp.OIDC
	// ldap is the directory simple-bind client (G7), nil when disabled.
	ldap *idp.LDAP
}

// NewRouter creates the gin engine and mounts all routes, and also hands out
// the config pusher so background tasks (alloc auto re-pointing) reuse the
// same push chain.
func NewRouter(db *gorm.DB, cfg config.Config) (*gin.Engine, *relaypush.Pusher) {
	h := &Handler{db: db, cfg: cfg, hub: agenthub.New()}
	// External admin logins are off unless the config turns them on (G7).
	if cfg.Oidc.Enabled {
		h.oidc = idp.NewOIDC(cfg.Oidc)
	}
	if cfg.Ldap.Enabled {
		h.ldap = idp.NewLDAP(cfg.Ldap)
	}
	h.redeemLimiter = ratelimit.New(ratelimit.Options{
		Window:      time.Minute,
		MaxAttempts: 10,
		FailLimit:   5,
		Lockout:     15 * time.Minute,
	})
	h.loginLimiter = ratelimit.New(ratelimit.Options{
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
	// MaxAttempts is set high: this limiter only uses failure-lockout semantics;
	// Allow is only used to query the lockout state.
	h.subMissLimiter = ratelimit.New(ratelimit.Options{
		Window:      time.Minute,
		MaxAttempts: 1 << 20,
		FailLimit:   20,
		Lockout:     15 * time.Minute,
	})
	h.enrollLimiter = ratelimit.New(ratelimit.Options{
		Window:      time.Minute,
		MaxAttempts: 10,
		FailLimit:   5,
		Lockout:     15 * time.Minute,
	})
	h.pusher = relaypush.New(db, h.hub, nil)
	h.secrets = secret.NewStore(cfg.SecretKey)
	r := gin.Default()
	// ClientIP only trusts forwarding headers from explicitly allow-listed
	// reverse proxies (audit fix 2026-11-12): by default no proxy is trusted
	// (ClientIP = direct address), so the IP basis of login/subscription/redeem
	// rate limiting and audit logs cannot be forged via X-Forwarded-For; behind
	// a reverse proxy, configure FERRY_TRUSTED_PROXIES with the actual CIDRs.
	// A broken allow-list falls back to trusting no proxy (fail-safe).
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Printf("trusted proxies %q invalid: %v; trusting none", cfg.TrustedProxies, err)
		_ = r.SetTrustedProxies(nil)
	}

	r.GET("/api/health", h.healthGuard(), h.health)
	r.GET("/agent/ws", h.agentWS)
	r.GET("/sub/:token", h.subscription)
	// Self-built subscriptions (ferry-sub-filters): new paths with an
	// independent token. gin prefers static segments over param segments at the
	// same level, so /sub/x/abc hits this route while /sub/abc still goes to the
	// main subscription (verified by build-and-run).
	r.GET("/sub/x/:token", h.subscriptionFiltered)
	r.GET("/feed.xml", h.feedXML)
	r.GET("/api/speedtest/bytes", h.speedtestBytes)

	// Admin data surface (security fix 2026-10-10): dash JWT or API Token,
	// either one; see apitoken.go apiAuth. Previously this group had no
	// middleware at all, leaving the admin endpoints exposed on the public net.
	// AU-1: write operations go through auditMiddleware into audit_logs
	// uniformly (GET skipped).
	api := r.Group("/api", h.apiAuth(), h.auditMiddleware())
	{
		api.GET("/nodes", h.listNodes)
		api.POST("/nodes", h.createNode)
		// One-time enrollment credentials (FERRY-001): issue/list/revoke on the admin surface, redeem in the open group.
		api.POST("/enrollment-tokens", h.createEnrollmentToken)
		api.GET("/enrollment-tokens", h.listEnrollmentTokens)
		api.DELETE("/enrollment-tokens/:id", h.revokeEnrollmentToken)
		api.GET("/nodes/:id", h.getNode)
		api.PUT("/nodes/:id", h.updateNode)
		api.DELETE("/nodes/:id", h.deleteNode)
		api.POST("/nodes/:id/token", h.rotateNodeToken)
		api.POST("/nodes/:id/token/revoke", h.revokeNodeToken)
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
		// Domain fronting (BR-2): DNS provider credentials and fronting records.
		api.GET("/dns-providers", h.listDNSProviders)
		api.POST("/dns-providers", h.createDNSProvider)
		api.PUT("/dns-providers/:id", h.updateDNSProvider)
		api.DELETE("/dns-providers/:id", h.deleteDNSProvider)
		api.GET("/dns-fronts", h.listDNSFronts)
		api.POST("/dns-fronts", h.createDNSFront)
		api.PUT("/dns-fronts/:id", h.updateDNSFront)
		api.DELETE("/dns-fronts/:id", h.deleteDNSFront)
		// Per-region reconciliation compensation entry (E-27): manual alignment after event-driven sync failure or hand DB edits.
		api.POST("/dns-fronts/geo-sync", h.geoSync)
		// Certificate tasks (BR-4): orchestration and manual triggering.
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
		// Event outbox (HERALD-1): pending/failed visible in dash, dead letters re-dispatched by hand.
		api.GET("/events", h.listEvents)
		api.POST("/events/:id/retry", h.retryEvent)
		// Channel delivery dashboard and self-check (HC-1/2/3): receipt aggregation +
		// health badge, self-check test events, sample previews for each alert
		// kind.
		api.GET("/events/deliveries", h.listEventDeliveries)
		api.GET("/events/samples", h.listEventSamples)
		api.POST("/events/test", h.testEvent)
		api.GET("/cost", h.getCost)
		api.GET("/evening", h.getEvening)
		api.PUT("/cost/threshold", h.putCostThreshold)
		// Cost reference library (E-31): price table import/trial lookup/batch deviation (reference prices only advise, never change the price).
		api.GET("/cost/ref", h.getCostRef)
		api.GET("/cost/ref-check", h.getCostRefCheck)
		api.PUT("/cost/ref-table", h.putCostRefTable)
		// Price watches (E-32): condition CRUD and snapshots (scan alerts in cost.WatchLoop).
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
		// Protocol registry (protocol pluginization): protocol data source for the dash node form.
		api.GET("/protocols", h.listProtocols)

		// Entry domain data surface (TOUCH-3): shared data source for the domain routine mail and outage resilience.
		api.GET("/entry-domains", h.listEntryDomains)
		api.POST("/entry-domains", h.createEntryDomain)
		api.PUT("/entry-domains/:id", h.updateEntryDomain)
		api.DELETE("/entry-domains/:id", h.deleteEntryDomain)
		// Outage marker (TOUCH-7): admin manual switch; the subscription notes pick up a warning line accordingly.
		api.GET("/outage", h.getOutage)
		api.PUT("/outage", h.putOutage)
		api.GET("/nodes/:id/share", h.nodeShare)
		api.GET("/nodes/:id/logs", h.nodeProcLogs)
		api.GET("/nodes/:id/procs", h.nodeProcs)
		api.POST("/nodes/:id/rulelib", h.pushRuleLib)
		api.GET("/nodes/:id/rulelib", h.listRuleLib)
		// Multi-level admin / sub-admin quotas (P2-2): the admin management
		// surface is super-only; operator (sub-admin) is bounded by the max_users
		// create-user quota (user.go).
		api.GET("/admins", h.requireSuper(), h.listAdmins)
		api.POST("/admins", h.requireSuper(), h.createAdmin)
		api.PUT("/admins/:id", h.requireSuper(), h.updateAdmin)
		api.DELETE("/admins/:id", h.requireSuper(), h.deleteAdmin)
		api.GET("/users", h.listUsers)
		api.POST("/users", h.createUser)
		api.GET("/users/export", h.exportUsers)
		api.POST("/users/import", h.importUsers)
		api.GET("/users/:id", h.getUser)
		api.PUT("/users/:id", h.updateUser)
		api.DELETE("/users/:id", h.deleteUser)
		api.POST("/users/:id/sub-token", h.resetSubToken)
		api.GET("/users/:id/devices", h.listUserDevices)
		api.GET("/users/:id/traffic", h.userTraffic)
		// Plans (F-0101): sellable package templates applied to users in bulk.
		api.GET("/plans", h.listPlans)
		api.POST("/plans", h.createPlan)
		api.GET("/plans/:id", h.getPlan)
		api.PUT("/plans/:id", h.updatePlan)
		api.DELETE("/plans/:id", h.deletePlan)
		api.POST("/plans/:id/apply", h.applyPlan)
		// Paths (F-0105/F-0243): named entry -> (optional relay) -> landing routes.
		api.GET("/paths", h.listPaths)
		api.POST("/paths", h.createPath)
		api.GET("/paths/:id", h.getPath)
		api.GET("/paths/:id/health", h.getPathHealth)
		api.PUT("/paths/:id", h.updatePath)
		api.DELETE("/paths/:id", h.deletePath)
		// Path groups (F-0105/F-0261): ordered candidate path sets with a default.
		api.GET("/path-groups", h.listPathGroups)
		api.POST("/path-groups", h.createPathGroup)
		api.GET("/path-groups/:id", h.getPathGroup)
		api.GET("/path-groups/:id/health", h.getPathGroupHealth)

		// Scoped delivery/allocation policies (F-0282~F-0296).
		api.GET("/policies", h.listPolicies)
		api.POST("/policies", h.createPolicy)
		api.PUT("/policies/:id", h.updatePolicy)
		api.DELETE("/policies/:id", h.deletePolicy)
		api.GET("/policies/resolve", h.resolvePolicyView)
		api.GET("/policies/simulate", h.simulatePolicy)
		api.PUT("/path-groups/:id", h.updatePathGroup)
		api.DELETE("/path-groups/:id", h.deletePathGroup)
		api.POST("/path-groups/:id/members", h.addPathGroupMember)
		api.PATCH("/path-groups/:id/members/:pathId", h.updatePathGroupMember)
		api.DELETE("/path-groups/:id/members/:pathId", h.removePathGroupMember)
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
		// Distributor agents (DS-1): account create/disable, commission ratio and ledger entries; payout comes with DS-2.
		api.GET("/distributors", h.listDistributors)
		api.POST("/distributors", h.createDistributor)
		api.PUT("/distributors/:id", h.updateDistributor)
		api.GET("/distributors/:id/ledger", h.distributorLedger)
		// Settlement payout and manual adjustment (DS-2): booked by hand, never auto-paid.
		api.POST("/distributors/:id/payout", h.distributorPayout)
		api.POST("/distributors/:id/adjust", h.distributorAdjust)
		// Online payment callback (PAY-8): mounted on the open group (carries Provider signature verification), see end of file.
		api.GET("/payments/reconcile", h.listReconcile)
		// Order detail and refund flow (OD-2)
		api.GET("/payments/orders/:order_no", h.adminOrderDetail)
		api.POST("/payments/orders/:order_no/refund", h.refundOrder)
		// In-site notification center (NT-1): dash announcement fan-out and the full list.
		api.GET("/notifications", h.listNotifications)
		api.POST("/notifications/announcement", h.createAnnouncement)
		api.GET("/notifications/announcements", h.listAnnouncements)
		api.POST("/notifications/announcements/:id/resend", h.resendAnnouncement)
		api.DELETE("/notifications/:id", h.deleteNotification)
		// P1-1 admin login and auth (API Token issuance follows the apiAuth dual-credential convention).
		api.GET("/token", h.GetCurrentUser)
		api.POST("/token", h.AdminGetApiToken)
		// Operation audit (AU-1): write operation log queries; admin writes land rows via auditMiddleware.
		api.GET("/audit-logs", h.ListAuditLogs)
		// Coupons (PROMO-1): management-surface CRUD; redemption happens on panel order placement (panel.go).
		api.GET("/coupons", h.listCoupons)
		api.POST("/coupons", h.createCoupon)
		api.PUT("/coupons/:id", h.updateCoupon)
		api.DELETE("/coupons/:id", h.deleteCoupon)
		// Limited-time campaigns (PROMO-2): management-surface CRUD; auto-application on panel order placement (best one wins).
		api.GET("/campaigns", h.listCampaigns)
		api.POST("/campaigns", h.createCampaign)
		api.PUT("/campaigns/:id", h.updateCampaign)
		api.DELETE("/campaigns/:id", h.deleteCampaign)
	}
	// Public surfaces carrying their own credentials/signatures: panel by
	// subscription token (panel.go), bot by service token (TOUCH-6), Herald
	// receipts and payment callbacks each verify their signature, standby by
	// shared token (P2-1 master/standby sync) -- they bypass apiAuth, hence a
	// separate group.
	open := r.Group("/api")
	{
		// Standby instance pulls the master panel snapshot (P2-1): X-Standby-Token
		// machine auth; without a configured token it always 403s (sync surface off).
		open.GET("/standby/snapshot", h.standbySnapshot)
		panel := open.Group("/panel")
		{
			panel.GET("/me", h.panelMe)
			panel.GET("/contact", h.panelContact)
			panel.PUT("/contact", h.panelContactPut)
			panel.GET("/savings", h.panelSavings)
			panel.POST("/redeem", h.panelRedeem)
			panel.GET("/orders", h.panelOrders)
			// Online ordering (PAY-11, against the epusdt section)
			panel.GET("/products", h.panelProducts)
			panel.GET("/channels", h.panelChannels)
			panel.GET("/campaigns", h.panelCampaigns)
			panel.POST("/orders", h.panelCreateOrder)
			panel.GET("/orders/:order_no", h.panelOrderStatus)
			// Notification center (NT-1) and preferences (NT-2)
			panel.GET("/notifications", h.panelNotifications)
			panel.GET("/notifications/unread-count", h.panelUnreadCount)
			panel.POST("/notifications/read-all", h.panelMarkAllRead)
			panel.POST("/notifications/:id/read", h.panelMarkRead)
			panel.GET("/notify-prefs", h.panelNotifyPrefs)
			panel.PUT("/notify-prefs", h.panelUpdateNotifyPrefs)
			// Support embedding config (servify real-embed acceptance): enabled=false when no support is wired up.
			panel.GET("/support", h.panelSupport)
		}
		// TG bot integration surface (TOUCH-6): service-level token auth, the bot
		// backend is deployed independently (does not depend on the panel domain
		// being alive), located by the user's tg_chat_id (TOUCH-1 binding).
		bot := open.Group("/bot")
		{
			bot.GET("/summary", h.botSummary)
		}
		// Channel dispatch receipts from Herald's async re-post (HERALD-2): carries its own HMAC verification.
		open.POST("/internal/event-results", h.eventResult)
		// First-install node registration exchange (FERRY-001): carries a one-time
		// enrollment credential (enroll.go); the admin surface /api/nodes stays
		// fully apiAuth-protected, this endpoint only swaps the credential for a node token.
		open.POST("/enroll", h.enrollNode)
		// Online payment callback (PAY-8): carries Provider signature verification.
		open.POST("/pay/epusdt/notify", h.epusdtNotify)
	}
	// Self-built subscription self-service surface (ferry-sub-filters): login by
	// subscription token (same convention as panelUser), users manage their own
	// independent subscriptions. The path shares the /api prefix but auth is a
	// subscription token rather than an admin JWT, hence a separate group
	// (without apiAuth/auditMiddleware).
	subs := r.Group("/api", h.panelAuth())
	{
		subs.GET("/subscriptions", h.listSubscriptions)
		subs.POST("/subscriptions", h.createSubscription)
		subs.PATCH("/subscriptions/:id", h.updateSubscription)
		subs.DELETE("/subscriptions/:id", h.deleteSubscription)
		subs.POST("/subscriptions/:id/reset-token", h.resetSubscriptionToken)
	}
	// P1-1 admin login is the only entry point to obtain the first token, mounted
	// publicly outside the auth groups (it was previously mis-mounted inside,
	// making the login surface 401-unreachable; fixed in the security batch).
	r.POST("/admin/login", h.AdminLogin)
	// G7 external admin logins (both off unless configured): the same public
	// mounting as the local login, since the callback needs no prior token.
	r.GET("/admin/login/available", h.adminLoginAvailable)
	r.GET("/admin/oidc/login", h.oidcLogin)
	r.GET("/admin/oidc/callback", h.oidcCallback)
	r.POST("/admin/login/ldap", h.ldapLogin)
	// DS-1 agent login: an independent endpoint mounted publicly; role=distributor tokens and admin tokens never cross boundaries.
	r.POST("/distributor/login", h.DistributorLogin)
	// DS-3 agent self-service surface: read-only views (own batches/codes/customers/orders/settlement).
	dist := r.Group("/distributor/api", h.distAuthMiddleware())
	{
		dist.GET("/me", h.distMe)
		dist.GET("/batches", h.distBatches)
		dist.GET("/batches/:id/codes", h.distBatchCodes)
		dist.GET("/customers", h.distCustomers)
		dist.GET("/orders", h.distOrders)
		dist.GET("/ledger", h.distLedger)
	}
	// AU-1: admin-surface write operations land in the audit log the same way (GET skipped).
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
		// Two-factor and login audit (security design §1, P1)
		admin.GET("/2fa", h.twofaStatus)
		admin.POST("/2fa/setup", h.twofaSetup)
		admin.POST("/2fa/enable", h.twofaEnable)
		admin.POST("/2fa/disable", h.twofaDisable)
		admin.GET("/login-logs", h.loginLogs)
	}
	return r, h.pusher
}

// errProbeRollback is the rollback sentinel of the DB write probe: a
// successful INSERT inside the transaction proves writability, then it is
// rolled back leaving no row; only a genuine probe failure (read-only disk /
// broken connection) counts as not writable.
var errProbeRollback = errors.New("health probe rollback")

// healthGuard is the access gate for /api/health (P2 fix): loopback probes
// are let through -- the start.sh readiness wait and the various smokes all
// probe from 127.0.0.1 before admin login, with no credential available;
// remote callers must carry admin-surface credentials (admin JWT or API
// Token) or get 401. Previously health hung with no auth, exposing runtime
// state such as agents_online / backup freshness / DB writability publicly.
func (h *Handler) healthGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if ip := net.ParseIP(c.ClientIP()); ip != nil && ip.IsLoopback() {
			c.Next()
			return
		}
		if h.authorizeAPI(c) {
			c.Next()
			return
		}
		msg := "令牌缺失"
		if bearerToken(c) != "" {
			msg = "无效令牌"
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": msg})
		c.Abort()
	}
}

// health runs the health self-check (panel availability §5, P1): DB
// writability, agent online count, backup freshness (age since the last
// persisted copy; older than FreshHours is not fresh). A failing check item
// is never silenced: status is reported as degraded honestly (still 200, so
// probes do not kill the node mistakenly).
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
		status = "degraded" // never backed up
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

// listDimensionStatus returns the status-light data for the region/ISP dimensions.
func (h *Handler) listDimensionStatus(c *gin.Context) {
	out := []storage.DimensionStatus{}
	if err := h.db.Order("scope, key").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// listProbeReports returns the edge probe verdict records (newest first, 200 by default).
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
