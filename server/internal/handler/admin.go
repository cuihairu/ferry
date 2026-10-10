package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// adminClaims 是 JWT claim，包含用户 ID、是否为管理员与管理员层级
//（P2-2：super=超级管理员 / operator=子管理员；空=旧令牌，按 super 兼容）。
type adminClaims struct {
	jwt.RegisteredClaims
	IsAdmin bool   `json:"is_admin"`
	Role    string `json:"role"`
}

// RoleSuper/RoleOperator 是管理员层级（users.admin_role 同值域）。
const (
	RoleSuper    = "super"
	RoleOperator = "operator"
)

// normalizeAdminRole 归一层级：空/未知按 super（旧管理员兼容）。
func normalizeAdminRole(r string) string {
	if r == RoleOperator {
		return RoleOperator
	}
	return RoleSuper
}

// AdminLogin 管理员登录（P1-1 + 安全设计 §1）：验证用户名/密码，绑定两步
// 验证者再校验 TOTP/恢复码，返回 signed JWT。失败按 IP 滑动窗口计数达阈值
// 锁定（复用兑换限速思路），每次尝试落 login_logs 审计，连续失败与新网段
// 成功经 Herald login_alert 告警；未绑定 2FA 者登录行为不变。
func (h *Handler) AdminLogin(c *gin.Context) {
	ip := c.ClientIP()
	ua := c.Request.UserAgent()
	// 速率限流：每 60 秒最多 5 次尝试，失败 5 次进入 15 分钟锁定
	if !h.redeemLimiter.Allow(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试过于频繁，请稍后再试"})
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTP     string `json:"totp"` // 6 位 TOTP 或一次性恢复码（绑定者必填）
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	u, err := h.findUserByUsername(in.Username)
	if err != nil {
		// 无论用户是否存在，均记录一次失败以防止枚举
		h.loginFailed(c, in.Username, ip, ua, "用户名或密码错误", "")
		return
	}
	if !u.Enabled {
		h.loginFailed(c, in.Username, ip, ua, "账户已停用", "")
		return
	}
	if !u.IsAdmin {
		h.loginFailed(c, in.Username, ip, ua, "无管理员权限", "")
		return
	}
	// password 校验：bcrypt 比对
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(in.Password)); err != nil {
		h.loginFailed(c, in.Username, ip, ua, "用户名或密码错误", "")
		return
	}
	// 二次因子（安全设计 §1）：绑定者必须给 6 位 TOTP 或恢复码；
	// 空值单独回码让 dash 补出验证码输入位，未绑定者直过。
	if u.TOTPEnabled && strings.TrimSpace(in.TOTP) == "" {
		h.loginFailed(c, in.Username, ip, ua, "需要两步验证码", "totp_required")
		return
	}
	if !h.checkTwoFA(&u, in.TOTP) {
		h.loginFailed(c, in.Username, ip, ua, "两步验证码错误", "totp_invalid")
		return
	}
	now := time.Now()
	expire := now.Add(24 * time.Hour) // 登录令牌有效期 24 小时
	claims := adminClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.Username,
			ID:        strconv.FormatUint(uint64(u.ID), 10),
			ExpiresAt: jwt.NewNumericDate(expire),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		IsAdmin: u.IsAdmin,
		Role:    normalizeAdminRole(u.AdminRole),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// 使用配置中的秘钥签名；默认使用 "ferry-admin-secret"，可通过 FERRY_ADMIN_SECRET 环境变量覆盖
	secret := h.cfg.AdminSecret
	if secret == "" {
		secret = "ferry-admin-secret"
	}
	ss, err := token.SignedString([]byte(secret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "内部错误"})
		return
	}
	// 审计落行 + 新网段告警（排除自身行取上次成功基线）。
	row := h.writeLoginLog(u.Username, ip, ua, true)
	h.alertLoginNewIP(u.Username, ip, row.ID)
	c.JSON(http.StatusOK, gin.H{"token": ss})
}

// loginFailed 登录失败统一收尾：限速计数、审计落行、连续失败告警检查，
// 然后回 401（code 可选机器可读标记，如 totp_required/totp_invalid）。
func (h *Handler) loginFailed(c *gin.Context, username, ip, ua, msg, code string) {
	h.redeemLimiter.RecordFailure(ip)
	h.writeLoginLog(username, ip, ua, false)
	h.alertLoginBrute(ip)
	body := gin.H{"error": msg}
	if code != "" {
		body["code"] = code
	}
	c.JSON(http.StatusUnauthorized, body)
}

// adminAuthMiddleware 管理员身份中间件：从 Authorization: Bearer <token> 解析并验证。
// 秘钥取 cfg.AdminSecret（FERRY_ADMIN_SECRET），缺省回落与 AdminLogin 签发侧一致
// （2026-10-10 修复：此前硬编码缺省值，配置 env 后中间件仍按缺省验签，直接打挂 /admin）。
func (h *Handler) adminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := bearerToken(c)
		if tokenStr == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "令牌缺失"})
			c.Abort()
			return
		}
		claims := &adminClaims{}
		secret := h.cfg.AdminSecret
		if secret == "" {
			secret = "ferry-admin-secret"
		}
		tkn, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		})
		// 只认 is_admin=true：代理令牌（role=distributor）与管理员同密钥
		// 签发，签名可验但非管理员身份——不查此位=代理可过 /admin 组。
		if err != nil || !tkn.Valid || !claims.IsAdmin {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "无效令牌"})
			c.Abort()
			return
		}
		c.Set("adminUserID", claims.ID)
		c.Set("adminIsAdmin", claims.IsAdmin)
		c.Set("adminRole", normalizeAdminRole(claims.Role))
		c.Set("adminUser", claims.Subject)
		c.Next()
	}
}

// findUserByUsername 根据用户名查找用户。
func (h *Handler) findUserByUsername(username string) (storage.User, error) {
	var u storage.User
	if err := h.db.Where("username = ?", username).First(&u).Error; err != nil {
		return u, err
	}
	return u, nil
}
