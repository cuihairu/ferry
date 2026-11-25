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

// Batch-21 gap fill: conventions of the API Token issue endpoint and the
// current-user endpoint (P1-2).
// Note: the blank-identity branches of both are tested against the real
// contract -- after apiAuth lets a request through, only an empty
// adminUserID/apiUser string is rejected (a nil key, i.e. key unset, goes
// down the API Token path, which is the pass-through case).

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

// blankIDAdminJWT signs an admin JWT with an empty ID: apiAuth lets it
// through (the signature is valid), and the endpoint rejects it for lacking
// an admin identity.
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

// TestAdminGetApiTokenRejectsBlankAdminID covers an admin JWT whose ID is
// blank: the middleware lets it through, and the endpoint rejects it for
// lacking an admin (the 403 branch).
func TestAdminGetApiTokenRejectsBlankAdminID(t *testing.T) {
	h, r := newApiTokenEnv(t)
	r.POST("/t/apitoken", h.apiAuth(), h.AdminGetApiToken)
	rec := postWithToken(t, r, http.MethodPost, "/t/apitoken", blankIDAdminJWT(t))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d; want 403", rec.Code)
	}
}

// TestAdminGetApiTokenIssuesAndAuths covers the issue-verify-authorize loop:
// an admin JWT obtains an API Token, an unset cfg secret verifies against the
// fallback secret (the fallback convention), and once the token passes the
// data-plane middleware GetCurrentUser reports apiUser.
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
	// An admin JWT likewise passes the data plane and retrieves the current user.
	rec3 := doJSON(t, r, "GET", "/t/me", nil)
	if rec3.Code != http.StatusOK {
		t.Fatalf("admin me: %d %s", rec3.Code, rec3.Body)
	}
}

// TestGetCurrentUserBlankSubject: an empty apiUser string is 401 rather than claiming to be logged in.
func TestGetCurrentUserBlankSubject(t *testing.T) {
	h, r := newApiTokenEnv(t)
	r.GET("/t/me", h.apiAuth(), h.GetCurrentUser)
	rec := postWithToken(t, r, http.MethodGet, "/t/me", blankIDAdminJWT(t))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d; want 401", rec.Code)
	}
}
