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

// Regression (found in the 2026-10-10 dogfood): the /api management data
// plane was previously unauthenticated as a whole group, so anyone could
// create users, delete nodes and read the full subscriber list. After the
// fix: missing credentials are 401, bad credentials are 401, an admin JWT and
// an API Token both pass, FERRY_ADMIN_SECRET takes effect, and the
// credentialed public surface (panel) is unaffected.

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
	// Sign an API Token with the same custom secret: the data plane should pass it.
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
	// An API Token signed with the default secret must be rejected once a custom one is configured.
	def := apiTokenClaims{RegisteredClaims: jwt.RegisteredClaims{
		Subject: "admin", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}}
	defSS, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, def).SignedString([]byte("ferry-api-secret"))
	if code := req401(t, r, "GET", "/api/users", "Bearer "+defSS); code != http.StatusUnauthorized {
		t.Fatalf("default-secret api token after env override = %d, want 401", code)
	}
}

func TestApiDataPlaneHonorsAdminSecretEnv(t *testing.T) {
	// Once FERRY_ADMIN_SECRET is configured: an admin JWT signed with that secret
	// passes, and the default secret is rejected (fixed 2026-10-10: the
	// middleware previously hardcoded the default secret, so the env was a no-op).
	// The root row is the pre-existing admin used to re-check the /admin group's
	// DB round-trip (seedAdminForJWT), with id=1.
	r, db := newTestRouterCfg(t, func(c *config.Config) { c.AdminSecret = "custom-admin-secret" })
	seedAdminForJWT(t, db)
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
	// The /admin group follows the same rule.
	if code := req401(t, r, "GET", "/admin/status", "Bearer "+adminJWT(t)); code != http.StatusUnauthorized {
		t.Fatalf("default-secret on /admin = %d, want 401", code)
	}
	if code := req401(t, r, "GET", "/admin/status", "Bearer "+ss); code != http.StatusOK {
		t.Fatalf("custom-secret on /admin = %d, want 200", code)
	}
}

func TestPanelUnaffectedByApiAuth(t *testing.T) {
	// panel sits in the open group: the subscription token identity keeps working,
	// unaffected by the data-plane authentication.
	r, _ := newTestRouterWithDB(t)
	token := panelToken(t, r, "grace")
	if code := req401(t, r, "GET", "/api/panel/me", "Bearer "+token); code != http.StatusOK {
		t.Fatalf("panel me with sub token = %d, want 200", code)
	}
	// Conversely: a subscription token is not a management credential and must not
	// cross into the data plane.
	if code := req401(t, r, "GET", "/api/users", "Bearer "+token); code != http.StatusUnauthorized {
		t.Fatalf("sub token on admin route = %d, want 401", code)
	}
}

func TestTokenIssuanceFlow(t *testing.T) {
	// POST /api/token mints an API Token with an admin JWT, after which the API
	// Token works (previously AdminGetApiToken could not read adminUserID and
	// always returned 403, leaving the endpoint dead).
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
	// GET /api/token reports the current identity.
	req2 := httptest.NewRequest("GET", "/api/token", nil)
	req2.Header.Set("Authorization", "Bearer "+apiToken)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET /api/token = %d, want 200", rec2.Code)
	}
}

func TestPublicCallbackFacesStillOpen(t *testing.T) {
	// Public surfaces (each with its own verification) are not blocked by the
	// data-plane authentication: a garbage epusdt callback body should reach the
	// business validation (not a 401 token error), and the Herald receipt
	// follows the same rule.
	r, _ := newTestRouterWithDB(t)
	req := httptest.NewRequest("POST", "/api/pay/epusdt/notify", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized && rec.Body.String() == `{"error":"无效令牌"}` {
		t.Fatalf("epusdt notify blocked by apiAuth: %d %s", rec.Code, rec.Body)
	}
}

func TestDistributorTokenRejectedOnAdminFaces(t *testing.T) {
	// Found in a 2026-10-10 live walkthrough: a distributor token is signed with
	// the same secret as an admin (DS-1), and apiAuth plus adminAuthMiddleware
	// only verified the signature without checking is_admin -- a distributor
	// token escalated straight through the management surface with full rights.
	// After the fix both surfaces return 401, and the distributor's own portal
	// is unaffected.
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
	// The distributor's own portal works as usual (the role=distributor check lives in that portal's middleware).
	if code := req401(t, r, "GET", "/distributor/api/me", "Bearer "+dist); code != http.StatusOK {
		t.Fatalf("dist token on own portal = %d, want 200", code)
	}
}

func TestDistributorPortalHonorsAdminSecretEnv(t *testing.T) {
	// Found in a 2026-10-10 live walkthrough: distAuthMiddleware verified
	// signatures against a hardcoded default constant, so once an operator set
	// FERRY_ADMIN_SECRET, issuance (the cfg secret) and verification diverged
	// and the whole distributor portal returned 401. After the fix issuance and
	// verification share the same source.
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
