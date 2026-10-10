package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
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
	if rec.Code != http.StatusOK {
		t.Fatalf("over-quota: %d", rec.Code)
	}
	// 空订阅仍带断联容灾注释（TOUCH-7）：无节点链接，逃生通道在手
	raw, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil || strings.Contains(string(raw), "vless://") || !strings.Contains(string(raw), "公告订阅") {
		t.Fatalf("over-quota should be nodeless sub with notes: %v %q", err, string(raw))
	}
	if !strings.Contains(rec.Header().Get("Subscription-Userinfo"), "download=60") {
		t.Fatalf("over-quota userinfo = %q", rec.Header().Get("Subscription-Userinfo"))
	}

	// 已到期：空订阅（仍带断联容灾注释）
	_, expTok := mkUser("carol", 0, "2020-01-01T00:00:00Z")
	if rec := getSub(t, r, "/sub/"+expTok, "v2rayNG/1.8"); rec.Code != http.StatusOK {
		t.Fatalf("expired: %d", rec.Code)
	} else if raw, err := base64.StdEncoding.DecodeString(rec.Body.String()); err != nil ||
		strings.Contains(string(raw), "vless://") || !strings.Contains(string(raw), "公告订阅") {
		t.Fatalf("expired should be nodeless sub with notes: %v %q", err, string(raw))
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
	raw, err = base64.StdEncoding.DecodeString(rec.Body.String())
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
	if raw3, err := base64.StdEncoding.DecodeString(r3.Body.String()); err != nil ||
		strings.Contains(string(raw3), "vless://") || !strings.Contains(string(raw3), "公告订阅") {
		t.Fatalf("窗口内超限应封订阅（注释仍在）: %v %q", err, r3.Body)
	}
}

// TestSubscriptionRateLimit 订阅端点限频（安全设计 §4）：
// token 层 30 次/分钟防单 token 爆破；miss 层按 IP 连续 20 次未知/停用令牌
// 锁 15 分钟防每次换新 token 的枚举；命中 429 并记日志（token 脱敏前缀）。
func TestSubscriptionRateLimit(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	tokenOf := func(name string) string {
		rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": name, "quota_bytes": 1000})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create user %s: %d %s", name, rec.Code, rec.Body)
		}
		var u map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &u)
		return u["sub_token"].(string)
	}
	alice, bob := tokenOf("rl-alice"), tokenOf("rl-bob")

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	// token 层：同令牌窗口 30 次内放行，第 31 次 429 并记日志。
	for i := 0; i < 30; i++ {
		if rec := getSub(t, r, "/sub/"+alice, "v2rayNG/1.8"); rec.Code != http.StatusOK {
			t.Fatalf("alice 第 %d 次应放行: %d", i+1, rec.Code)
		}
	}
	if rec := getSub(t, r, "/sub/"+alice, "v2rayNG/1.8"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("alice 第 31 次应 429: %d", rec.Code)
	}
	if !strings.Contains(buf.String(), "sub rate limited") {
		t.Fatalf("命中限频应记日志: %q", buf.String())
	}
	// 按 token 隔离：alice 预算耗尽不影响 bob。
	if rec := getSub(t, r, "/sub/"+bob, "v2rayNG/1.8"); rec.Code != http.StatusOK {
		t.Fatalf("bob 应不受 alice 耗尽影响: %d", rec.Code)
	}

	// miss 层：同 IP 连续 20 个未知令牌（各不相同）后进入锁定，
	// 第 21 个未知令牌 429，同 IP 的有效令牌也被锁。
	for i := 0; i < 20; i++ {
		if rec := getSub(t, r, fmt.Sprintf("/sub/enum-probe-%02d", i), "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
			t.Fatalf("未知令牌第 %d 次应 404: %d", i+1, rec.Code)
		}
	}
	if rec := getSub(t, r, "/sub/enum-probe-lock", "v2rayNG/1.8"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("枚举锁定后应 429: %d", rec.Code)
	}
	if rec := getSub(t, r, "/sub/"+bob, "v2rayNG/1.8"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("锁定按 IP 生效，有效令牌同锁: %d", rec.Code)
	}
}

// TestSubscriptionHysteria2 覆盖 hy2 节点全链（hy2 批）：建节点校验
// （缺密码 400）→ 订阅链接（hysteria2://）→ clash（type: hysteria2）→
// singbox（outbounds 数组）。
func TestSubscriptionHysteria2(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 建节点：缺密码被拦（面板明确报错，不静默）。
	if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "bad", "address": "a", "port": 443, "protocol": "hysteria2", "config": `{"sni":"s.com"}`,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("hy2 missing password: %d %s", rec.Code, rec.Body)
	}
	// 建节点：全字段 hy2。
	if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "hy", "address": "hy.example.com", "port": 443, "protocol": "hysteria2",
		"transport": "quic",
		"config":    `{"password":"pw","sni":"s.com","obfs":"salamander","obfs_password":"op","up":100,"down":200}`,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create hy2 node: %d %s", rec.Code, rec.Body)
	}
	if err := db.Model(&storage.Node{}).Where("name=?", "hy").
		Updates(map[string]any{"role": "entry", "meta_init": true}).Error; err != nil {
		t.Fatal(err)
	}

	// 用户 + 订阅。
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "hyuser"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	token := u["sub_token"].(string)
	subPath := "/sub/" + token

	// v2ray target：hysteria2:// 链接（全字段断言）。
	raw, err := base64.StdEncoding.DecodeString(getSub(t, r, subPath, "NekoBox/1.0").Body.String())
	if err != nil {
		t.Fatalf("body not base64: %v", err)
	}
	wantLink := "hysteria2://pw@hy.example.com:443?down=200&obfs=salamander&obfs-password=op&sni=s.com&up=100"
	if n := strings.Count(string(raw), wantLink); n != 1 {
		t.Fatalf("expected 1 hy2 link, got %d: %q", n, string(raw))
	}

	// clash target：type: hysteria2 条目与字段名。
	body := getSub(t, r, subPath+"?target=clash", "curl/8.0").Body.String()
	for _, want := range []string{"type: hysteria2", "password: pw", "obfs: salamander", "obfs-password: op", "up: 100", "down: 200", "sni: s.com"} {
		if !strings.Contains(body, want) {
			t.Fatalf("clash body missing %q:\n%s", want, body)
		}
	}

	// singbox target：outbounds 数组带 hysteria2 出站。
	sb := getSub(t, r, subPath+"?target=singbox", "curl/8.0")
	if sb.Code != http.StatusOK {
		t.Fatalf("singbox target: %d %s", sb.Code, sb.Body)
	}
	var obs []map[string]any
	if err := json.Unmarshal(sb.Body.Bytes(), &obs); err != nil {
		t.Fatalf("singbox not json array: %v\n%s", err, sb.Body)
	}
	if len(obs) != 1 || obs[0]["type"] != "hysteria2" || obs[0]["password"] != "pw" {
		t.Fatalf("singbox outbounds = %v", obs)
	}

	// routing 合成对 hy2 如实报错（xray 语法不适用 YAML 内核）。
	if rec = doJSON(t, r, "GET", "/api/nodes/1/routing-config", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("routing render on hy2: %d %s", rec.Code, rec.Body)
	}
}
