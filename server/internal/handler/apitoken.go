package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/ratelimit"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// apiTokenClaims is the JWT claim of an API Token.
type apiTokenClaims struct {
	jwt.RegisteredClaims
}

// ApiTokenLimiter is the API Token IP rate limiter (P1-2: 100 times per hour per IP).
var ApiTokenLimiter = ratelimit.New(ratelimit.Options{
	Window:      time.Hour,
	MaxAttempts: 100,
	FailLimit:   20,
	Lockout:     60 * time.Minute,
})

// apiTokenSecretFallback is the default secret used when FERRY_API_SECRET is
// unset (same behavior as historically); once configured, cfg.ApiTokenSecret
// takes precedence (fixed 2026-10-10: the middleware side previously
// hardcoded the default, making the env var useless).
const apiTokenSecretFallback = "ferry-api-secret"

// AdminGetApiToken lets an admin generate an API Token (P1-2): admins only; returns a signed JWT.
func (h *Handler) AdminGetApiToken(c *gin.Context) {
	// Admins only.
	adminID, _ := c.Get("adminUserID")
	if adminID == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
		return
	}
	// Rate limiting: 100 times per hour per IP.
	ip := c.ClientIP()
	if !ApiTokenLimiter.Allow(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试过于频繁，请稍后再试"})
		return
	}
	token, err := h.generateApiToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成令牌失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"api_token": token})
}

// generateApiToken generates an API Token (P1-2).
func (h *Handler) generateApiToken() (string, error) {
	now := time.Now()
	expire := now.Add(30 * 24 * time.Hour) // Token validity: 30 days.
	claims := apiTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "admin",
			ExpiresAt: jwt.NewNumericDate(expire),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	secret := h.cfg.ApiTokenSecret
	if secret == "" {
		secret = apiTokenSecretFallback
	}
	ss, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", err
	}
	return ss, nil
}

// bearerToken takes the Bearer payload from the Authorization header (empty = absent).
func bearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return ""
}

// apiAuth is the /api management data-plane middleware (security fix
// 2026-10-10): the entire /api group (all management routes such as
// nodes/users/cards/distributors) was previously left unauthenticated, with
// only /api/token gated by API Token verification -- the management surface
// was effectively public. It now accepts two credentials: an admin JWT
// (signed by /admin/login, used by the dash) or an API Token (P1-2
// automation); both secrets come from cfg (FERRY_ADMIN_SECRET / FERRY_API_SECRET
// take effect). Credentialed public surfaces (panel subscription tokens, bot
// service tokens, Herald receipt verification, payment callback verification)
// live in a separate open group and do not pass through this middleware.
func (h *Handler) apiAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.authorizeAPI(c) {
			c.Next()
			return
		}
		msg := "无效令牌"
		if bearerToken(c) == "" {
			msg = "令牌缺失"
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": msg})
		c.Abort()
	}
}

// authorizeAPI verifies management-plane credentials (reusable in P2 for
// middleware-free paths such as health): a valid admin JWT or API Token sets
// the context and returns true; on failure it leaves the context untouched and
// returns false (no response written, so the caller decides how to finish).
func (h *Handler) authorizeAPI(c *gin.Context) bool {
	tokenStr := bearerToken(c)
	if tokenStr == "" {
		return false
	}
	adminSecret := h.cfg.AdminSecret
	if adminSecret == "" {
		adminSecret = "ferry-admin-secret"
	}
	adminClaims := &adminClaims{}
	// Only is_admin=true is accepted: a distributor token is signed with the
	// same secret as an admin (DS-1), so the signature verifies but the
	// identity is not admin -- without this check a distributor token would
	// pass straight through into the management data plane.
	if tkn, err := jwt.ParseWithClaims(tokenStr, adminClaims, func(t *jwt.Token) (interface{}, error) {
		return []byte(adminSecret), nil
	}); err == nil && tkn.Valid && adminClaims.IsAdmin {
		c.Set("adminUserID", adminClaims.ID)
		c.Set("adminIsAdmin", adminClaims.IsAdmin)
		// Admin tier (P2-2): requireSuper thresholds and operator quota accounting read from this.
		c.Set("adminRole", normalizeAdminRole(adminClaims.Role))
		c.Set("apiUser", adminClaims.Subject)
		return true
	}
	apiSecret := h.cfg.ApiTokenSecret
	if apiSecret == "" {
		apiSecret = apiTokenSecretFallback
	}
	apiClaims := &apiTokenClaims{}
	if tkn, err := jwt.ParseWithClaims(tokenStr, apiClaims, func(t *jwt.Token) (interface{}, error) {
		return []byte(apiSecret), nil
	}); err == nil && tkn.Valid {
		c.Set("apiUser", apiClaims.Subject)
		return true
	}
	return false
}

// GetCurrentUser is the current API user (P1-2): reads the logged-in API username from the context.
func (h *Handler) GetCurrentUser(c *gin.Context) {
	user, _ := c.Get("apiUser")
	if user == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"username": user})
}
