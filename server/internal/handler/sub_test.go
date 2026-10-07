package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// getSub 以指定 UA 请求订阅端点。
func getSub(t *testing.T, r *gin.Engine, path, ua string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("User-Agent", ua)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSubscription(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 用户 alice：配额 1000，2030 到期
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "alice", "quota_bytes": 1000, "expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	token := u["sub_token"].(string)

	// 节点：hk 可用（vless+ws+tls），us 停用
	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "hk", "address": "a.example.com", "port": 443, "protocol": "vless",
		"config": `{"uuid":"u-1","tls":true,"sni":"s.com","net":"ws","path":"/wss","host":"cdn.example.com"}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "us", "address": "b.example.com", "port": 443, "protocol": "trojan",
		"config": `{"password":"pw"}`, "enabled": false,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create disabled node: %d %s", rec.Code, rec.Body)
	}
	// E-19：仅入口/双角色节点进订阅，落地不下发（测试 API 建节点默认 landing）。
	if err := db.Model(&storage.Node{}).Where("name=?", "hk").
		Updates(map[string]any{"role": "entry", "meta_init": true}).Error; err != nil {
		t.Fatal(err)
	}

	subPath := "/sub/" + token

	// 默认 UA（v2ray 系）：base64 链接，只含可用节点
	rec = getSub(t, r, subPath, "v2rayNG/1.8")
	if rec.Code != http.StatusOK {
		t.Fatalf("sub default: %d %s", rec.Code, rec.Body)
	}
	raw, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil {
		t.Fatalf("body not base64: %v (%s)", err, rec.Body)
	}
	if n := strings.Count(string(raw), "vless://u-1@a.example.com:443"); n != 1 {
		t.Fatalf("expected 1 vless link, got %d: %q", n, string(raw))
	}
	if strings.Contains(string(raw), "trojan://") || strings.Contains(string(raw), "b.example.com") {
		t.Fatalf("disabled node leaked: %q", string(raw))
	}
	if info := rec.Header().Get("Subscription-Userinfo"); !strings.Contains(info, "total=1000") || !strings.Contains(info, "expire=") {
		t.Fatalf("userinfo header = %q", info)
	}
	if rec.Header().Get("Profile-Update-Interval") == "" || rec.Header().Get("Profile-Title") != "alice" {
		t.Fatalf("profile headers missing: %v", rec.Header())
	}

	// clash UA：YAML 输出
	clashRec := getSub(t, r, subPath, "clash-verge/2.0")
	if clashRec.Code != http.StatusOK {
		t.Fatalf("sub clash: %d", clashRec.Code)
	}
	body := clashRec.Body.String()
	if !strings.Contains(body, "proxies:") || !strings.Contains(body, "type: vless") || !strings.Contains(body, "MATCH,PROXY") {
		t.Fatalf("clash body incomplete:\n%s", body)
	}
	if strings.Contains(body, "name: us") {
		t.Fatalf("disabled node in clash yaml:\n%s", body)
	}
	// ws 传输展开
	if !strings.Contains(body, "ws-opts:") || !strings.Contains(body, "path: /wss") {
		t.Fatalf("ws-opts missing:\n%s", body)
	}

	// target 显式覆盖 UA
	if rec := getSub(t, r, subPath+"?target=clash", "curl/8.0"); !strings.Contains(rec.Body.String(), "proxies:") {
		t.Fatalf("target=clash ignored:\n%s", rec.Body)
	}
	if rec := getSub(t, r, subPath+"?target=bad", "curl/8.0"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad target: %d", rec.Code)
	}

	// 错误令牌 404
	if rec := getSub(t, r, "/sub/nope", "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("bad token: %d", rec.Code)
	}

	// 用户停用后令牌视同吊销：404
	if rec := doJSON(t, r, "PUT", "/api/users/1", map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("disable user: %d", rec.Code)
	}
	if rec := getSub(t, r, subPath, "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled user should 404: %d", rec.Code)
	}
}

func TestSubscriptionAvailability(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "hk", "address": "a", "port": 443, "protocol": "vless", "config": `{"uuid":"u-1"}`,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d", rec.Code)
	}
	if err := db.Model(&storage.Node{}).Where("name=?", "hk").
		Updates(map[string]any{"role": "entry", "meta_init": true}).Error; err != nil {
		t.Fatal(err)
	}

	mkUser := func(name string, quota int64, expires string) (uint, string) {
		body := map[string]any{"username": name, "quota_bytes": quota}
		if expires != "" {
			body["expires_at"] = expires
		}
		rec := doJSON(t, r, "POST", "/api/users", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body)
		}
		var u map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &u)
		id := uint(u["id"].(float64))
		return id, u["sub_token"].(string)
	}

	// 超限（120/100）：空订阅但保留用量头
	quotaID, quotaTok := mkUser("bob", 100, "")
	if err := db.Create(&storage.TrafficLog{UserID: quotaID, RxBytes: 60, TxBytes: 60, RecordedAt: time.Now()}).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}
	rec := getSub(t, r, "/sub/"+quotaTok, "v2rayNG/1.8")
	if rec.Code != http.StatusOK || rec.Body.String() != "" {
		t.Fatalf("over-quota should be empty sub: %d %q", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Header().Get("Subscription-Userinfo"), "download=60") {
		t.Fatalf("over-quota userinfo = %q", rec.Header().Get("Subscription-Userinfo"))
	}

	// 已到期：空订阅
	_, expTok := mkUser("carol", 0, "2020-01-01T00:00:00Z")
	if rec := getSub(t, r, "/sub/"+expTok, "v2rayNG/1.8"); rec.Code != http.StatusOK || rec.Body.String() != "" {
		t.Fatalf("expired should be empty sub: %d %q", rec.Code, rec.Body)
	}

	// 不限配额（0）有用量：照常出节点
	freeID, freeTok := mkUser("dave", 0, "")
	if err := db.Create(&storage.TrafficLog{UserID: freeID, RxBytes: 1, TxBytes: 2, RecordedAt: time.Now()}).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}
	rec = getSub(t, r, "/sub/"+freeTok, "v2rayNG/1.8")
	if rec.Code != http.StatusOK {
		t.Fatalf("unlimited user: %d", rec.Code)
	}
	raw, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil || !strings.Contains(string(raw), "vless://u-1@a:443") {
		t.Fatalf("unlimited user should get node: %v %q", err, string(raw))
	}
	if info := rec.Header().Get("Subscription-Userinfo"); !strings.Contains(info, "upload=1") || !strings.Contains(info, "download=2") {
		t.Fatalf("userinfo = %q", info)
	}
}

// TestSubResetCycleWindow 是 P1-4 语义：day 周期用户窗口外流量不计超限，
// 窗口内超限才封订阅。
func TestSubResetCycleWindow(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "hk", "address": "a.example.com", "port": 443, "protocol": "vless",
		"config": `{"uuid":"u-1","tls":true,"sni":"s.com","net":"ws","path":"/wss","host":"cdn.example.com"}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d", rec.Code)
	}
	if err := db.Model(&storage.Node{}).Where("name=?", "hk").
		Updates(map[string]any{"role": "entry", "meta_init": true}).Error; err != nil {
		t.Fatal(err)
	}

	rec = doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "cycled", "quota_bytes": 100, "reset_cycle": "day",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	tok := u["sub_token"].(string)

	// 上月 200 超 100：day 窗口外 → 照常出节点，用量头只算窗口内
	if err := db.Create(&storage.TrafficLog{
		UserID: uint(u["id"].(float64)), RxBytes: 200,
		RecordedAt: time.Now().AddDate(0, -1, 0),
	}).Error; err != nil {
		t.Fatal(err)
	}
	r1 := getSub(t, r, "/sub/"+tok, "v2rayNG/1.8")
	if r1.Code != http.StatusOK || r1.Body.String() == "" {
		t.Fatalf("窗口外流量不应封订阅: %d %q", r1.Code, r1.Body)
	}

	// 今日再记 60：窗口内 60 < 100 → 仍出节点
	if err := db.Create(&storage.TrafficLog{
		UserID: uint(u["id"].(float64)), RxBytes: 60, RecordedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	r2 := getSub(t, r, "/sub/"+tok, "v2rayNG/1.8")
	if r2.Code != http.StatusOK || r2.Body.String() == "" {
		t.Fatalf("窗口内未超限不应封订阅: %d %q", r2.Code, r2.Body)
	}
	if !strings.Contains(r2.Header().Get("Subscription-Userinfo"), "total=100") ||
		strings.Contains(r2.Header().Get("Subscription-Userinfo"), "download=200") {
		t.Fatalf("用量头应只算窗口内: %q", r2.Header().Get("Subscription-Userinfo"))
	}

	// 今日累计到 100 → 封
	if err := db.Create(&storage.TrafficLog{
		UserID: uint(u["id"].(float64)), RxBytes: 40, RecordedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	r3 := getSub(t, r, "/sub/"+tok, "v2rayNG/1.8")
	if r3.Body.String() != "" {
		t.Fatalf("窗口内超限应封订阅: %q", r3.Body)
	}
}
