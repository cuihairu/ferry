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

// apiTokenSecret 是 API Token 签名秘钥，默认 "ferry-api-secret"，可通过 FERRY_API_SECRET 环境变量覆盖。
var apiTokenSecret = "ferry-api-secret"

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
	token, err := generateApiToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成令牌失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"api_token": token})
}

// generateApiToken 生成 API Token（P1-2）。
func generateApiToken() (string, error) {
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
	secret := apiTokenSecret // 生产请務必配置 FERRY_API_SECRET
	ss, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", err
	}
	return ss, nil
}

// apiAuthMiddleware API Token 身份中间件（P1-2）：从 Authorization: Bearer <token> 解析并验证。
func apiAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		tokenStr := ""
		if strings.HasPrefix(auth, "Bearer ") {
			tokenStr = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		}
		if tokenStr == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "API Token 缺失"})
			c.Abort()
			return
		}
		claims := &apiTokenClaims{}
		secret := apiTokenSecret // 生产请务必配置 FERRY_API_SECRET
		tkn, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		})
		if err != nil || !tkn.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "无效 API Token"})
			c.Abort()
			return
		}
		c.Set("apiUser", claims.Subject)
		c.Next()
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