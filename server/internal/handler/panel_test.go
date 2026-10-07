package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// doPanel 以订阅令牌为身份发请求（PAY-7）：token 为空则不带凭据。
func doPanel(t *testing.T, r *gin.Engine, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// panelToken 建用户并取其订阅令牌。
func panelToken(t *testing.T, r *gin.Engine, username string) string {
	t.Helper()
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": username, "quota_bytes": 1 << 30})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	return u["sub_token"].(string)
}

func TestPanelMe(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	token := panelToken(t, r, "carol")

	// 令牌命中：返回身份与用量口径
	rec := doPanel(t, r, token, "GET", "/api/panel/me", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	var me struct {
		Username   string `json:"username"`
		QuotaBytes int64  `json:"quota_bytes"`
		UsedBytes  int64  `json:"used_bytes"`
		SubToken   string `json:"sub_token"`
		Active     bool   `json:"active"`
		OverQuota  bool   `json:"over_quota"`
		ExpiresAt  any    `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if me.Username != "carol" || me.QuotaBytes != 1<<30 || me.Active != true {
		t.Fatalf("me = %+v", me)
	}
	if me.OverQuota || me.ExpiresAt != nil || me.SubToken != token {
		t.Fatalf("me fields = %+v", me)
	}

	// 无凭据 / 未知令牌：404（防枚举，与 /sub 口径一致）
	if rec := doPanel(t, r, "", "GET", "/api/panel/me", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := doPanel(t, r, "not-a-token", "GET", "/api/panel/me", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", rec.Code)
	}

	// 停用用户：令牌视同吊销，404
	if rec := doJSON(t, r, "PUT", "/api/users/1", map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("disable user: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, token, "GET", "/api/panel/me", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled user: %d", rec.Code)
	}
}

func TestPanelRedeem(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "dave", map[string]any{
		"grant_type": "add_quota", "grant_value": 2 << 30,
	}, 2)
	var u storage.User
	if err := db.First(&u, userID).Error; err != nil {
		t.Fatal(err)
	}

	// 兑换：身份取自令牌，载荷只带 code（user_id 不可传）
	rec := doPanel(t, r, u.SubToken, "POST", "/api/panel/redeem", map[string]any{"code": codes[0]})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
	var res struct {
		GrantType  string `json:"grant_type"`
		QuotaBytes int64  `json:"quota_bytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.GrantType != "add_quota" || res.QuotaBytes != 1000+2<<30 {
		t.Fatalf("result = %+v", res)
	}

	// 同码复用：统一失败文案；X-Ferry-Token 头同样可作凭据
	rec = doPanel(t, r, u.SubToken, "POST", "/api/panel/redeem", map[string]any{"code": codes[0]})
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("兑换失败")) {
		t.Fatalf("reuse: %d %s", rec.Code, rec.Body)
	}
	req := httptest.NewRequest("POST", "/api/panel/redeem", bytes.NewBufferString(`{"code":"`+codes[1]+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ferry-Token", u.SubToken)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("header token: %d %s", rec2.Code, rec2.Body)
	}

	// 无凭据：404，不落到兑换逻辑
	if rec := doPanel(t, r, "", "POST", "/api/panel/redeem", map[string]any{"code": "ZZZZ-ZZZZ-ZZZZ"}); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
}
