package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// 回归（2026-10-10 dogfood 发现）：/api 管理数据面此前整组无鉴权，
// 任意人可建用户/删节点/读全量订阅者。修后：无凭据 401、坏凭据 401、
// 管理员 JWT 与 API Token 双凭据放行、FERRY_ADMIN_SECRET 生效、
// 自带凭据的公开面（panel）不受影响。

func req401(t *testing.T, r *gin.Engine, method, path, auth string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

func TestApiDataPlaneRejectsMissingAndGarbageAuth(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if code := req401(t, r, "GET", "/api/users", ""); code != http.StatusUnauthorized {
		t.Fatalf("no auth GET /api/users = %d, want 401", code)
	}
	if code := req401(t, r, "POST", "/api/users", "Bearer garbage-token-xyz"); code != http.StatusUnauthorized {
		t.Fatalf("garbage auth POST /api/users = %d, want 401", code)
	}
	if code := req401(t, r, "DELETE", "/api/users/1", "Bearer garbage-token-xyz"); code != http.StatusUnauthorized {
		t.Fatalf("garbage auth DELETE /api/users/1 = %d, want 401", code)
	}
}

func TestApiDataPlaneAcceptsAdminJwt(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if code := req401(t, r, "GET", "/api/users", "Bearer "+adminJWT(t)); code != http.StatusOK {
		t.Fatalf("admin jwt GET /api/users = %d, want 200", code)
	}
}

func TestApiDataPlaneAcceptsApiToken(t *testing.T) {
	r, _ := newTestRouterCfg(t, func(c *config.Config) { c.ApiTokenSecret = "custom-api-secret" })
	// 用同一自定义秘钥签 API Token：数据面应放行。
	claims := apiTokenClaims{RegisteredClaims: jwt.RegisteredClaims{
		Subject:   "admin",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}}
	ss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("custom-api-secret"))
	if err != nil {
		t.Fatalf("sign api token: %v", err)
	}
	if code := req401(t, r, "GET", "/api/users", "Bearer "+ss); code != http.StatusOK {
		t.Fatalf("api token GET /api/users = %d, want 200", code)
	}
	// 默认秘钥签的 API Token 在配置了自定义秘钥后必须被拒。
	def := apiTokenClaims{RegisteredClaims: jwt.RegisteredClaims{
		Subject: "admin", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}}
	defSS, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, def).SignedString([]byte("ferry-api-secret"))
	if code := req401(t, r, "GET", "/api/users", "Bearer "+defSS); code != http.StatusUnauthorized {
		t.Fatalf("default-secret api token after env override = %d, want 401", code)
	}
}

func TestApiDataPlaneHonorsAdminSecretEnv(t *testing.T) {
	// FERRY_ADMIN_SECRET 配置后：该秘钥签的管理员 JWT 放行，缺省秘钥被拒
	//（2026-10-10 修复：此前中间件硬编码缺省秘钥，env 形同虚设）。
	r, _ := newTestRouterCfg(t, func(c *config.Config) { c.AdminSecret = "custom-admin-secret" })
	custom := adminClaims{RegisteredClaims: jwt.RegisteredClaims{
		ID: "1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}, IsAdmin: true}
	ss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, custom).SignedString([]byte("custom-admin-secret"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if code := req401(t, r, "GET", "/api/users", "Bearer "+ss); code != http.StatusOK {
		t.Fatalf("custom-secret admin jwt = %d, want 200", code)
	}
	if code := req401(t, r, "GET", "/api/users", "Bearer "+adminJWT(t)); code != http.StatusUnauthorized {
		t.Fatalf("default-secret admin jwt after env override = %d, want 401", code)
	}
	// /admin 组同一口径。
	if code := req401(t, r, "GET", "/admin/status", "Bearer "+adminJWT(t)); code != http.StatusUnauthorized {
		t.Fatalf("default-secret on /admin = %d, want 401", code)
	}
	if code := req401(t, r, "GET", "/admin/status", "Bearer "+ss); code != http.StatusOK {
		t.Fatalf("custom-secret on /admin = %d, want 200", code)
	}
}

func TestPanelUnaffectedByApiAuth(t *testing.T) {
	// panel 挂 open 组：订阅令牌身份照常工作，不受数据面鉴权影响。
	r, _ := newTestRouterWithDB(t)
	token := panelToken(t, r, "grace")
	if code := req401(t, r, "GET", "/api/panel/me", "Bearer "+token); code != http.StatusOK {
		t.Fatalf("panel me with sub token = %d, want 200", code)
	}
	// 反向：订阅令牌不是管理凭据，不得越过数据面。
	if code := req401(t, r, "GET", "/api/users", "Bearer "+token); code != http.StatusUnauthorized {
		t.Fatalf("sub token on admin route = %d, want 401", code)
	}
}

func TestTokenIssuanceFlow(t *testing.T) {
	// POST /api/token 用管理员 JWT 签发 API Token，随后 API Token 可用
	//（此前 AdminGetApiToken 读不到 adminUserID 恒 403，端点死挂）。
	r, _ := newTestRouterWithDB(t)
	req := httptest.NewRequest("POST", "/api/token", nil)
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/token = %d %s, want 200", rec.Code, rec.Body)
	}
	var issued struct {
		APIToken string `json:"api_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil || issued.APIToken == "" {
		t.Fatalf("decode api_token: %v %s", err, rec.Body)
	}
	apiToken := issued.APIToken
	if code := req401(t, r, "GET", "/api/users", "Bearer "+apiToken); code != http.StatusOK {
		t.Fatalf("minted api token GET /api/users = %d, want 200", code)
	}
	// GET /api/token 回当前身份。
	req2 := httptest.NewRequest("GET", "/api/token", nil)
	req2.Header.Set("Authorization", "Bearer "+apiToken)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET /api/token = %d, want 200", rec2.Code)
	}
}

func TestPublicCallbackFacesStillOpen(t *testing.T) {
	// 公开面（各自验签）不被数据面鉴权挡住：epusdt 回调垃圾体应到业务校验
	//（非 401 令牌错误），Herald 回执同口径。
	r, _ := newTestRouterWithDB(t)
	req := httptest.NewRequest("POST", "/api/pay/epusdt/notify", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized && rec.Body.String() == `{"error":"无效令牌"}` {
		t.Fatalf("epusdt notify blocked by apiAuth: %d %s", rec.Code, rec.Body)
	}
}

func TestDistributorTokenRejectedOnAdminFaces(t *testing.T) {
	// 2026-10-10 实机走查发现：代理令牌与管理员同密钥签发（DS-1），apiAuth
	// 与 adminAuthMiddleware 只验签名不查 is_admin——代理令牌直通管理面
	// 全权提升。修后两面均 401，代理自面不受影响。
	r, _ := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d9", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create distributor: %d %s", rec.Code, rec.Body)
	}
	dist := distLogin(t, r, "d9", "secret123")
	if code := req401(t, r, "GET", "/api/distributors", "Bearer "+dist); code != http.StatusUnauthorized {
		t.Fatalf("dist token on /api/distributors = %d, want 401", code)
	}
	if code := req401(t, r, "GET", "/api/users", "Bearer "+dist); code != http.StatusUnauthorized {
		t.Fatalf("dist token on /api/users = %d, want 401", code)
	}
	if code := req401(t, r, "DELETE", "/api/nodes/1", "Bearer "+dist); code != http.StatusUnauthorized {
		t.Fatalf("dist token DELETE /api/nodes/1 = %d, want 401", code)
	}
	if code := req401(t, r, "GET", "/admin/status", "Bearer "+dist); code != http.StatusUnauthorized {
		t.Fatalf("dist token on /admin/status = %d, want 401", code)
	}
	// 代理自面照常（role=distributor 校验在自面中间件）。
	if code := req401(t, r, "GET", "/distributor/api/me", "Bearer "+dist); code != http.StatusOK {
		t.Fatalf("dist token on own portal = %d, want 200", code)
	}
}

func TestDistributorPortalHonorsAdminSecretEnv(t *testing.T) {
	// 2026-10-10 实机走查发现：distAuthMiddleware 验签硬编码缺省常量，
	// 运营设 FERRY_ADMIN_SECRET 后签发（cfg 秘钥）与验签错位，代理自面
	// 整面 401。修后签发与验签同源。
	r, _ := newTestRouterCfg(t, func(c *config.Config) { c.AdminSecret = "custom-admin-secret" })
	custom := adminClaims{RegisteredClaims: jwt.RegisteredClaims{
		ID: "1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}, IsAdmin: true}
	adminSS, err := jwt.NewWithClaims(jwt.SigningMethodHS256, custom).SignedString([]byte("custom-admin-secret"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	body := `{"username":"d8","password":"secret123"}`
	req := httptest.NewRequest("POST", "/api/distributors", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminSS)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create distributor: %d %s", rec.Code, rec.Body)
	}
	dist := distLogin(t, r, "d8", "secret123")
	if code := req401(t, r, "GET", "/distributor/api/me", "Bearer "+dist); code != http.StatusOK {
		t.Fatalf("dist me with env secret = %d, want 200", code)
	}
}
