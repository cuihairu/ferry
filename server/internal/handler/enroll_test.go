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

// enrollIssueToken 测试助手：管理员侧签发一次性注册凭据，返回凭据串与行 ID。
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

// doEnroll 发 /api/enroll 注册请求：Authorization 带注册凭据（不带管理 JWT）。
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

// enrollValidNodeBody 合法节点注册载荷（与安装脚本发的同形状）。
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

// TestEnrollRejectsMissingOrBadCredential：无凭据/坏凭据一律 401，不建节点。
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

// TestEnrollExchange：合法凭据换出节点令牌；一次性凭据重放被拒且不重复建节点。
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

	// 重放同一凭据：401 且不建第二个节点。
	if rec := doEnroll(t, r, tok, enrollValidNodeBody()); rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay enroll status = %d want 401", rec.Code)
	}
	if n := countNodes(t, r); n != 1 {
		t.Fatalf("nodes created = %d want 1", n)
	}

	// 凭据行已记消费痕迹（used_at/used_by 留档）。
	var row storage.EnrollmentToken
	if err := db.Where("token = ?", tok).First(&row).Error; err != nil {
		t.Fatalf("load enrollment token row: %v", err)
	}
	if row.UsedAt == nil || row.UsedBy != "hk-1" {
		t.Fatalf("enrollment token not marked used: used_at=%v used_by=%q", row.UsedAt, row.UsedBy)
	}
}

// TestEnrollExpiredAndRevoked：过期与已吊销凭据均 401。
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

// TestEnrollBadPayloadDoesNotConsume：载荷校验失败 400，凭据不被烧掉。
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

// TestEnrollmentTokenAdminAPIAuth：签发/列表/吊销都在管理数据面，无凭据 401。
func TestEnrollmentTokenAdminAPIAuth(t *testing.T) {
	r := newTestRouter(t)
	if rec := doBare(t, r, "POST", "/api/enrollment-tokens"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bare issue status = %d want 401", rec.Code)
	}
	if rec := doBare(t, r, "GET", "/api/enrollment-tokens"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bare list status = %d want 401", rec.Code)
	}
}

// TestEnrollmentTokenTTLValidation：ttl_minutes 非法 400；缺省 60 分钟。
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

// TestRevokeUsedEnrollmentToken：已消费的凭据不可再吊销（409，消费态是终态）。
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

// TestListEnrollmentTokensMasksSecret：列表不回带凭据明文（仅签发响应可见）。
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
