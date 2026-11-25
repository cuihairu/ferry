package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// enrollIssueToken test helper: issues a one-time enrollment credential from the
// admin side, returning the credential string and the row ID.
func enrollIssueToken(t *testing.T, r *gin.Engine, body any) (token string, id uint) {
	t.Helper()
	rec := doJSON(t, r, "POST", "/api/enrollment-tokens", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue enrollment token status = %d body=%s", rec.Code, rec.Body)
	}
	var out struct {
		ID    uint   `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode enrollment token resp: %v", err)
	}
	if out.Token == "" || out.ID == 0 {
		t.Fatalf("issued enrollment token must carry id and token: %+v", out)
	}
	return out.Token, out.ID
}

// doEnroll sends an /api/enroll registration request: Authorization carries the
// enrollment credential (not an admin JWT).
func doEnroll(t *testing.T, r *gin.Engine, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest("POST", "/api/enroll", &buf)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// enrollValidNodeBody a valid node registration payload (same shape the install script sends).
func enrollValidNodeBody() map[string]any {
	return map[string]any{
		"name": "hk-1", "address": "hk.example.com", "port": 443, "protocol": "vless",
	}
}

func countNodes(t *testing.T, r *gin.Engine) int {
	t.Helper()
	rec := doJSON(t, r, "GET", "/api/nodes", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list nodes status = %d body=%s", rec.Code, rec.Body)
	}
	var nodes []storage.Node
	if err := json.Unmarshal(rec.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("decode nodes resp: %v", err)
	}
	return len(nodes)
}

// TestEnrollRejectsMissingOrBadCredential: no credential / bad credential is always 401, no node created.
func TestEnrollRejectsMissingOrBadCredential(t *testing.T) {
	r := newTestRouter(t)
	for _, bearer := range []string{"", "not-a-real-token"} {
		rec := doEnroll(t, r, bearer, enrollValidNodeBody())
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("enroll bearer=%q status = %d want 401 body=%s", bearer, rec.Code, rec.Body)
		}
	}
	if n := countNodes(t, r); n != 0 {
		t.Fatalf("nodes created = %d want 0", n)
	}
}

// TestEnrollExchange: a valid credential exchanges for a node token; replaying a
// one-time credential is refused and no second node is created.
func TestEnrollExchange(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	tok, _ := enrollIssueToken(t, r, map[string]any{"note": "hk1 首装"})

	rec := doEnroll(t, r, tok, enrollValidNodeBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("enroll status = %d want 201 body=%s", rec.Code, rec.Body)
	}
	var node storage.Node
	if err := json.Unmarshal(rec.Body.Bytes(), &node); err != nil {
		t.Fatalf("decode node resp: %v", err)
	}
	if node.ID == 0 || node.Token == "" {
		t.Fatalf("enrolled node must carry id and agent token: %+v", node)
	}
	if node.Name != "hk-1" {
		t.Fatalf("node name = %q", node.Name)
	}

	// Replaying the same credential: 401 and no second node created.
	if rec := doEnroll(t, r, tok, enrollValidNodeBody()); rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay enroll status = %d want 401", rec.Code)
	}
	if n := countNodes(t, r); n != 1 {
		t.Fatalf("nodes created = %d want 1", n)
	}

	// The credential row carries consumption traces (used_at/used_by kept on file).
	var row storage.EnrollmentToken
	if err := db.Where("token = ?", tok).First(&row).Error; err != nil {
		t.Fatalf("load enrollment token row: %v", err)
	}
	if row.UsedAt == nil || row.UsedBy != "hk-1" {
		t.Fatalf("enrollment token not marked used: used_at=%v used_by=%q", row.UsedAt, row.UsedBy)
	}
}

// TestEnrollExpiredAndRevoked: expired and revoked credentials are both 401.
func TestEnrollExpiredAndRevoked(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	expired, _ := enrollIssueToken(t, r, map[string]any{})
	if err := db.Model(&storage.EnrollmentToken{}).
		Where("token = ?", expired).
		Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("expire token: %v", err)
	}
	if rec := doEnroll(t, r, expired, enrollValidNodeBody()); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired enroll status = %d want 401", rec.Code)
	}

	revoked, revokedID := enrollIssueToken(t, r, map[string]any{})
	if rec := doJSON(t, r, "DELETE", "/api/enrollment-tokens/"+strconv.Itoa(int(revokedID)), nil); rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d body=%s", rec.Code, rec.Body)
	}
	if rec := doEnroll(t, r, revoked, enrollValidNodeBody()); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked enroll status = %d want 401", rec.Code)
	}
	if n := countNodes(t, r); n != 0 {
		t.Fatalf("nodes created = %d want 0", n)
	}
}

// TestEnrollBadPayloadDoesNotConsume: payload validation failure is 400 and does not burn the credential.
func TestEnrollBadPayloadDoesNotConsume(t *testing.T) {
	r := newTestRouter(t)
	tok, _ := enrollIssueToken(t, r, map[string]any{})

	bad := enrollValidNodeBody()
	bad["port"] = 0
	if rec := doEnroll(t, r, tok, bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad payload status = %d want 400 body=%s", rec.Code, rec.Body)
	}

	rec := doEnroll(t, r, tok, enrollValidNodeBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid enroll after 400 status = %d want 201 body=%s", rec.Code, rec.Body)
	}
}

// TestEnrollmentTokenAdminAPIAuth: issue/list/revoke all live on the admin data plane; no credential is 401.
func TestEnrollmentTokenAdminAPIAuth(t *testing.T) {
	r := newTestRouter(t)
	if rec := doBare(t, r, "POST", "/api/enrollment-tokens"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bare issue status = %d want 401", rec.Code)
	}
	if rec := doBare(t, r, "GET", "/api/enrollment-tokens"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bare list status = %d want 401", rec.Code)
	}
}

// TestEnrollmentTokenTTLValidation: an invalid ttl_minutes is 400; the default is 60 minutes.
func TestEnrollmentTokenTTLValidation(t *testing.T) {
	r := newTestRouter(t)
	for _, ttl := range []int{-1, 0, 1441} {
		rec := doJSON(t, r, "POST", "/api/enrollment-tokens", map[string]any{"ttl_minutes": ttl})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("ttl=%d status = %d want 400 body=%s", ttl, rec.Code, rec.Body)
		}
	}

	rec := doJSON(t, r, "POST", "/api/enrollment-tokens", map[string]any{"note": "n1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("default ttl issue status = %d body=%s", rec.Code, rec.Body)
	}
	var out struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode issue resp: %v", err)
	}
	if min := time.Until(out.ExpiresAt).Minutes(); min < 55 || min > 65 {
		t.Fatalf("default ttl = %.1fmin want ~60min", min)
	}
}

// TestRevokeUsedEnrollmentToken: an already-consumed credential cannot be revoked again (409, consumption is a terminal state).
func TestRevokeUsedEnrollmentToken(t *testing.T) {
	r := newTestRouter(t)
	tok, id := enrollIssueToken(t, r, map[string]any{})
	if rec := doEnroll(t, r, tok, enrollValidNodeBody()); rec.Code != http.StatusCreated {
		t.Fatalf("enroll status = %d want 201", rec.Code)
	}
	rec := doJSON(t, r, "DELETE", "/api/enrollment-tokens/"+strconv.Itoa(int(id)), nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("revoke used status = %d want 409 body=%s", rec.Code, rec.Body)
	}
}

// TestListEnrollmentTokensMasksSecret: the listing does not carry credential plaintext (only the issue response does).
func TestListEnrollmentTokensMasksSecret(t *testing.T) {
	r := newTestRouter(t)
	tok, _ := enrollIssueToken(t, r, map[string]any{"note": "secret-mask"})

	rec := doJSON(t, r, "GET", "/api/enrollment-tokens", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(tok)) {
		t.Fatal("list response must not carry the enrollment token plaintext")
	}
}
