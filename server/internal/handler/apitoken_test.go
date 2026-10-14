package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// 二十一批补口：API Token 签发端与当前用户端（P1-2）口径。
// 注：两端的空身份分支按真实契约测——apiAuth 放行后 adminUserID/apiUser
// 为空串才拒（nil 未置键走 API 令牌路径，属放行面）。

func newApiTokenEnv(t *testing.T) (*Handler, *gin.Engine) {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	h := &Handler{db: db, cfg: config.Default()}
	return h, gin.New()
}

// blankIDAdminJWT 签一枚 ID 为空的管理员 JWT：apiAuth 放行（签名有效），
// 端点按无管理员身份拒。
func blankIDAdminJWT(t *testing.T) string {
	t.Helper()
	claims := adminClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "",
			ID:        "",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		IsAdmin: true,
	}
	ss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("ferry-admin-secret"))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return ss
}

func postWithToken(t *testing.T, r *gin.Engine, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestAdminGetApiTokenRejectsBlankAdminID 管理员 JWT 但 ID 为空：中间件
// 放行、端点按无管理员拒（403 分支）。
func TestAdminGetApiTokenRejectsBlankAdminID(t *testing.T) {
	h, r := newApiTokenEnv(t)
	r.POST("/t/apitoken", h.apiAuth(), h.AdminGetApiToken)
	rec := postWithToken(t, r, http.MethodPost, "/t/apitoken", blankIDAdminJWT(t))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d; want 403", rec.Code)
	}
}

// TestAdminGetApiTokenIssuesAndAuths 签发-验签-鉴权闭环：管理员 JWT 取得
// API Token，cfg 未配秘钥走缺省秘钥验签（fallback 口径），令牌过数据面
// 中间件后 GetCurrentUser 报出 apiUser。
func TestAdminGetApiTokenIssuesAndAuths(t *testing.T) {
	h, r := newApiTokenEnv(t)
	r.POST("/t/apitoken", h.apiAuth(), h.AdminGetApiToken)
	rec := doJSON(t, r, "POST", "/t/apitoken", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("issue: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		APIToken string `json:"api_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.APIToken == "" {
		t.Fatalf("body = %s err=%v", rec.Body, err)
	}
	claims := &apiTokenClaims{}
	if _, err := jwt.ParseWithClaims(out.APIToken, claims, func(*jwt.Token) (any, error) {
		return []byte(apiTokenSecretFallback), nil
	}); err != nil || claims.Subject != "admin" {
		t.Fatalf("claims = %+v err=%v", claims, err)
	}

	r.GET("/t/me", h.apiAuth(), h.GetCurrentUser)
	rec2 := postWithToken(t, r, http.MethodGet, "/t/me", out.APIToken)
	if rec2.Code != http.StatusOK || !strings.Contains(rec2.Body.String(), "admin") {
		t.Fatalf("api-token me: %d %s", rec2.Code, rec2.Body)
	}
	// 管理员 JWT 同样过数据面取到当前用户。
	rec3 := doJSON(t, r, "GET", "/t/me", nil)
	if rec3.Code != http.StatusOK {
		t.Fatalf("admin me: %d %s", rec3.Code, rec3.Body)
	}
}

// TestGetCurrentUserBlankSubject apiUser 为空串即 401，不冒称已登录。
func TestGetCurrentUserBlankSubject(t *testing.T) {
	h, r := newApiTokenEnv(t)
	r.GET("/t/me", h.apiAuth(), h.GetCurrentUser)
	rec := postWithToken(t, r, http.MethodGet, "/t/me", blankIDAdminJWT(t))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d; want 401", rec.Code)
	}
}
