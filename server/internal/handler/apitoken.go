package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/ratelimit"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// apiTokenClaims 是 API Token 的 JWT claim。
type apiTokenClaims struct {
	jwt.RegisteredClaims
}

// ApiTokenLimiter 是 API Token 的 IP 限流器（P1-2：每 IP 100 次/小时）。
var ApiTokenLimiter = ratelimit.New(ratelimit.Options{
	Window:      time.Hour,
	MaxAttempts: 100,
	FailLimit:   20,
	Lockout:     60 * time.Minute,
})

// apiTokenSecretFallback 是 FERRY_API_SECRET 未配置时的缺省秘钥（与历史
// 行为一致）；配置后以 cfg.ApiTokenSecret 为准（2026-10-10 修复：此前
// 中间件侧硬编码缺省值，env 形同虚设）。
const apiTokenSecretFallback = "ferry-api-secret"

// AdminGetApiToken 管理员生成 API Token（P1-2）：仅管理员可调，返回 signed JWT。
func (h *Handler) AdminGetApiToken(c *gin.Context) {
	// 仅管理员可访问
	adminID, _ := c.Get("adminUserID")
	if adminID == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
		return
	}
	// 速率限流：每 IP 100 次/小时
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

// generateApiToken 生成 API Token（P1-2）。
func (h *Handler) generateApiToken() (string, error) {
	now := time.Now()
	expire := now.Add(30 * 24 * time.Hour) // Token 有效期 30 天
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

// bearerToken 从 Authorization 头取 Bearer 载荷（空串=未携带）。
func bearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return ""
}

// apiAuth 是 /api 管理数据面中间件（2026-10-10 安全修复）：此前整个
// /api 组（nodes/users/cards/distributors 等全部管理路由）裸挂无鉴权，
// 仅 /api/token 挂 API Token 校验——管理面等同公网开放。现接受两种凭据：
// 管理员 JWT（/admin/login 签发，dash 使用）或 API Token（P1-2 自动化），
// 两者秘钥均取 cfg（FERRY_ADMIN_SECRET / FERRY_API_SECRET 生效）。
// 自带凭据的公开面（panel 订阅令牌、bot 服务令牌、Herald 回执验签、
// 支付回调验签）挂独立 open 组，不经此中间件。
func (h *Handler) apiAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := bearerToken(c)
		if tokenStr == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "令牌缺失"})
			c.Abort()
			return
		}
		adminSecret := h.cfg.AdminSecret
		if adminSecret == "" {
			adminSecret = "ferry-admin-secret"
		}
		adminClaims := &adminClaims{}
		// 只认 is_admin=true：代理令牌与管理员同密钥签发（DS-1），签名可验
		// 但非管理员身份——不查此位=代理令牌直通管理数据面。
		if tkn, err := jwt.ParseWithClaims(tokenStr, adminClaims, func(t *jwt.Token) (interface{}, error) {
			return []byte(adminSecret), nil
		}); err == nil && tkn.Valid && adminClaims.IsAdmin {
			c.Set("adminUserID", adminClaims.ID)
			c.Set("adminIsAdmin", adminClaims.IsAdmin)
			c.Set("apiUser", adminClaims.Subject)
			c.Next()
			return
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
			c.Next()
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "无效令牌"})
		c.Abort()
	}
}

// GetCurrentUser 当前 API 用户（P1-2）：从上下文获取当前登录的 API 用户名。
func (h *Handler) GetCurrentUser(c *gin.Context) {
	user, _ := c.Get("apiUser")
	if user == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"username": user})
}
