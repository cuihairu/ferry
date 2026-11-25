package handler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// External admin logins (G7): OIDC authorization-code login and LDAP
// simple-bind login, alongside the local password login. Both are off unless
// explicitly enabled in the config (config default off), and a disabled surface
// answers 404 -- the feature is simply not there. A successful external login
// issues the same admin JWT as the local login (issueAdminToken), so the
// session integration is the existing admin middleware with no second identity
// model.

// oidcStateTTL bounds the OIDC state round-trip: short enough that a captured
// state is stale before a provider exchange is plausible.
const oidcStateTTL = 5 * time.Minute

// adminSecret resolves the admin JWT signing secret, defaulting to the
// historical constant when unset.
func (h *Handler) adminSecret() string {
	if h.cfg.AdminSecret != "" {
		return h.cfg.AdminSecret
	}
	return "ferry-admin-secret"
}

// adminLoginAvailable reports whether either external login surface is enabled,
// so the dash login page can offer the links instead of dead buttons.
func (h *Handler) adminLoginAvailable(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"password": true,
		"oidc":     h.cfg.Oidc.Enabled,
		"ldap":     h.cfg.Ldap.Enabled,
	})
}

// oidcLogin starts the OIDC authorization-code flow (GET /admin/oidc/login).
// state is signed with the admin secret and carries an optional same-origin
// return path, so the provider redirect cannot smuggle a cross-site target.
func (h *Handler) oidcLogin(c *gin.Context) {
	if !h.cfg.Oidc.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	nonce, err := randomHex(16)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	returnTo := sameOriginPath(c.Query("redirect"))
	claims := &oidcStateClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "oidc-state",
			ID:        nonce,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(oidcStateTTL)),
		},
		ReturnTo: returnTo,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	state, err := token.SignedString([]byte(h.adminSecret()))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.Redirect(http.StatusFound, h.oidc.AuthorizeURL(state))
}

// oidcCallback finishes the flow (GET /admin/oidc/callback): verify the state,
// exchange the code, resolve the provider identity to an admin account and hand
// back the same JWT the local login issues. With a return path the token is
// pushed into the browser as a URL fragment (a cookie would widen the CSRF
// surface of the admin API), otherwise the token is returned as JSON.
func (h *Handler) oidcCallback(c *gin.Context) {
	if !h.cfg.Oidc.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	state := c.Query("state")
	claims, err := h.verifyOidcState(state)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的登录状态"})
		return
	}
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少授权码"})
		return
	}
	ident, err := h.oidc.Exchange(code)
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	username := ident.Username
	if username == "" {
		username = ident.Subject
	}
	u, err := h.findUserByUsername(username)
	if err != nil || !u.Enabled || !u.IsAdmin {
		// The account is unknown or not an admin: the same 404-style answer as
		// the local login for a missing account, without naming which.
		c.JSON(http.StatusForbidden, gin.H{"error": "该身份没有管理员账户"})
		return
	}
	token, err := h.issueAdminToken(&u)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	row := h.writeLoginLog(u.Username, c.ClientIP(), c.Request.UserAgent(), true)
	h.alertLoginNewIP(u.Username, c.ClientIP(), row.ID)
	if returnTo := sameOriginPath(claims.ReturnTo); returnTo != "" {
		c.Redirect(http.StatusFound, returnTo+"#oidc="+url.QueryEscape(token))
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token})
}

// sameOriginPath keeps the OIDC return target on this origin: anything else is
// dropped, including a protocol-relative "//host" value, which the browser would
// otherwise turn into an open redirect.
func sameOriginPath(p string) string {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return ""
	}
	return p
}

// ldapLogin authenticates against the directory (POST /admin/login/ldap) and
// issues the admin JWT. Rate limited and audited like the local login.
func (h *Handler) ldapLogin(c *gin.Context) {
	ip := c.ClientIP()
	if !h.cfg.Ldap.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if !h.loginLimiter.Allow(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试过于频繁，请稍后再试"})
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if in.Username == "" || in.Password == "" {
		h.loginFailed(c, in.Username, ip, c.Request.UserAgent(), "用户名或密码错误", "")
		return
	}
	if err := h.ldap.Auth(in.Username, in.Password); err != nil {
		h.loginFailed(c, in.Username, ip, c.Request.UserAgent(), "目录认证失败", "")
		return
	}
	u, err := h.findUserByUsername(in.Username)
	if err != nil || !u.Enabled || !u.IsAdmin {
		h.loginFailed(c, in.Username, ip, c.Request.UserAgent(), "无管理员权限", "")
		return
	}
	token, err := h.issueAdminToken(&u)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "内部错误"})
		return
	}
	row := h.writeLoginLog(u.Username, ip, c.Request.UserAgent(), true)
	h.alertLoginNewIP(u.Username, ip, row.ID)
	c.JSON(http.StatusOK, gin.H{"token": token})
}

// oidcStateClaims is the signed state round-trip of the OIDC flow.
type oidcStateClaims struct {
	jwt.RegisteredClaims
	ReturnTo string `json:"return_to,omitempty"`
}

// verifyOidcState parses and validates the signed OIDC state.
func (h *Handler) verifyOidcState(state string) (*oidcStateClaims, error) {
	if state == "" {
		return nil, errors.New("missing state")
	}
	claims := &oidcStateClaims{}
	tkn, err := jwt.ParseWithClaims(state, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(h.adminSecret()), nil
	})
	if err != nil || !tkn.Valid || claims.Subject != "oidc-state" {
		return nil, errors.New("invalid state")
	}
	return claims, nil
}

// randomHex returns n random bytes as hex (the OIDC state nonce).
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
