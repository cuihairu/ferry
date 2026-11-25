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

// adminClaims is a JWT claim set holding the user ID, admin flag, and admin
// tier (P2-2: super=super admin / operator=sub admin; empty=legacy token,
// treated as super for compatibility).
type adminClaims struct {
	jwt.RegisteredClaims
	IsAdmin bool   `json:"is_admin"`
	Role    string `json:"role"`
}

// RoleSuper/RoleOperator are admin tiers (same domain as users.admin_role).
const (
	RoleSuper    = "super"
	RoleOperator = "operator"
)

// normalizeAdminRole normalizes the tier: empty/unknown becomes super (legacy admin compatibility).
func normalizeAdminRole(r string) string {
	if r == RoleOperator {
		return RoleOperator
	}
	return RoleSuper
}

// AdminLogin is admin login (P1-1 + security design §1): verify username/password,
// then for accounts with a bound authenticator verify the TOTP/recovery code,
// and return a signed JWT. Failures are counted in a per-IP sliding window and
// lock out past the threshold (reusing the redemption rate-limit idea); every
// attempt is written to login_logs for audit, and consecutive failures plus
// successful logins from new subnets raise a Herald login_alert; accounts
// without 2FA keep their previous login behavior unchanged.
func (h *Handler) AdminLogin(c *gin.Context) {
	ip := c.ClientIP()
	ua := c.Request.UserAgent()
	// Rate limiting: at most 5 attempts per 60 seconds, then a 15-minute lockout after 5 failures
	if !h.loginLimiter.Allow(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试过于频繁，请稍后再试"})
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTP     string `json:"totp"` // 6-digit TOTP or one-time recovery code (required for bound accounts)
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	u, err := h.findUserByUsername(in.Username)
	if err != nil {
		// Always record a failure whether or not the user exists, to prevent enumeration
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
	// password check: bcrypt comparison
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(in.Password)); err != nil {
		h.loginFailed(c, in.Username, ip, ua, "用户名或密码错误", "")
		return
	}
	// second factor (security design §1): bound accounts must supply a 6-digit
	// TOTP or recovery code; an empty value gets its own code back so the dash
	// UI can add a verification-code input, while unbound accounts pass straight
	// through.
	if u.TOTPEnabled && strings.TrimSpace(in.TOTP) == "" {
		h.loginFailed(c, in.Username, ip, ua, "需要两步验证码", "totp_required")
		return
	}
	if !h.checkTwoFA(&u, in.TOTP) {
		h.loginFailed(c, in.Username, ip, ua, "两步验证码错误", "totp_invalid")
		return
	}
	now := time.Now()
	expire := now.Add(24 * time.Hour) // login token lifetime of 24 hours
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
	// Sign with the configured secret; defaults to "ferry-admin-secret" and can be overridden by the FERRY_ADMIN_SECRET env var
	secret := h.cfg.AdminSecret
	if secret == "" {
		secret = "ferry-admin-secret"
	}
	ss, err := token.SignedString([]byte(secret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "内部错误"})
		return
	}
	// Audit row + new-subnet alert (previous successful baseline excludes this row itself).
	row := h.writeLoginLog(u.Username, ip, ua, true)
	h.alertLoginNewIP(u.Username, ip, row.ID)
	c.JSON(http.StatusOK, gin.H{"token": ss})
}

// loginFailed is the common tail for a failed login: rate-limit counting, an audit
// row, and a consecutive-failure alert check, then reply 401 (code is an optional
// machine-readable marker, e.g. totp_required/totp_invalid).
func (h *Handler) loginFailed(c *gin.Context, username, ip, ua, msg, code string) {
	h.loginLimiter.RecordFailure(ip)
	h.writeLoginLog(username, ip, ua, false)
	h.alertLoginBrute(ip)
	body := gin.H{"error": msg}
	if code != "" {
		body["code"] = code
	}
	c.JSON(http.StatusUnauthorized, body)
}

// adminAuthMiddleware is the admin identity middleware: parse and verify the token
// from Authorization: Bearer <token>. The secret comes from cfg.AdminSecret
// (FERRY_ADMIN_SECRET), with a default fallback matching the AdminLogin signing side
// (2026-10-10 fix: this was previously hardcoded, so after setting the env var the
// middleware still verified against the default and broke /admin outright).
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
		// Only honor is_admin=true: distributor tokens (role=distributor) are signed with
		// the same key as admin tokens, so the signature verifies even though the
		// identity is not an admin -- skipping this check would let distributors pass
		// the /admin group.
		if err != nil || !tkn.Valid || !claims.IsAdmin {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "无效令牌"})
			c.Abort()
			return
		}
		// Re-check against the DB (audit fix 2026-11-12): a valid signature only proves the
		// token was legitimate at issue time, so after an account is disabled or
		// demoted the old token must not survive until natural expiry. Password
		// changes also invalidate it: tokens issued before the latest password change
		// are rejected (password_changed_at; pre-existing rows hold NULL and are not
		// judged).
		adminID, idErr := strconv.ParseUint(claims.ID, 10, 64)
		var u storage.User
		if idErr != nil || h.db.First(&u, uint(adminID)).Error != nil ||
			!u.Enabled || !u.IsAdmin ||
			(u.PasswordChangedAt != nil && claims.IssuedAt != nil &&
				claims.IssuedAt.Time.Before(*u.PasswordChangedAt)) {
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

// findUserByUsername looks up a user by username.
func (h *Handler) findUserByUsername(username string) (storage.User, error) {
	var u storage.User
	if err := h.db.Where("username = ?", username).First(&u).Error; err != nil {
		return u, err
	}
	return u, nil
}
