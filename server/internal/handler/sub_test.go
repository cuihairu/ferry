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

// getSub requests the subscription endpoint with the given UA.
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

	// User alice: quota 1000, expiring in 2030
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "alice", "quota_bytes": 1000, "expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	token := u["sub_token"].(string)

	// Nodes: hk available (vless+ws+tls), us disabled
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
	// E-19: only entry/both-role nodes enter the subscription, landing nodes are not
	// delivered (the test API creates nodes as landing by default).
	if err := db.Model(&storage.Node{}).Where("name=?", "hk").
		Updates(map[string]any{"role": "entry", "meta_init": true}).Error; err != nil {
		t.Fatal(err)
	}

	subPath := "/sub/" + token

	// Default UA (v2ray family): base64 links, only available nodes
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

	// clash UA: YAML output
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
	// ws transport expanded
	if !strings.Contains(body, "ws-opts:") || !strings.Contains(body, "path: /wss") {
		t.Fatalf("ws-opts missing:\n%s", body)
	}

	// sing-box/Stash UA: outbounds JSON; the default UA falls back to v2ray base64 (SHOP-2).
	for _, ua := range []string{"sing-box/1.8.0", "Stash/2.0"} {
		rec := getSub(t, r, subPath, ua)
		if rec.Code != http.StatusOK {
			t.Fatalf("sub %s: %d", ua, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"type": "vless"`) || strings.Contains(body, "proxies:") {
			t.Fatalf("singbox body for %s:\n%s", ua, body)
		}
	}
	if _, err := base64.StdEncoding.DecodeString(getSub(t, r, subPath, "curl/8.0").Body.String()); err != nil {
		t.Fatalf("default UA should v2ray base64: %v", err)
	}

	// target explicitly overrides the UA
	if rec := getSub(t, r, subPath+"?target=clash", "curl/8.0"); !strings.Contains(rec.Body.String(), "proxies:") {
		t.Fatalf("target=clash ignored:\n%s", rec.Body)
	}
	if rec := getSub(t, r, subPath+"?target=bad", "curl/8.0"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad target: %d", rec.Code)
	}

	// Bad token: 404
	if rec := getSub(t, r, "/sub/nope", "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("bad token: %d", rec.Code)
	}

	// After the user is disabled the token counts as revoked: 404
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

	// Over quota (120/100): empty subscription but the usage headers are kept
	quotaID, quotaTok := mkUser("bob", 100, "")
	qid := quotaID
	if err := db.Create(&storage.TrafficLog{UserID: &qid, RxBytes: 60, TxBytes: 60, RecordedAt: time.Now()}).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}
	rec := getSub(t, r, "/sub/"+quotaTok, "v2rayNG/1.8")
	if rec.Code != http.StatusOK {
		t.Fatalf("over-quota: %d", rec.Code)
	}
	// An empty subscription still carries the outage contingency notes (TOUCH-7):
	// no node links, but the escape channel is in hand
	raw, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil || strings.Contains(string(raw), "vless://") || !strings.Contains(string(raw), "公告订阅") {
		t.Fatalf("over-quota should be nodeless sub with notes: %v %q", err, string(raw))
	}
	if !strings.Contains(rec.Header().Get("Subscription-Userinfo"), "download=60") {
		t.Fatalf("over-quota userinfo = %q", rec.Header().Get("Subscription-Userinfo"))
	}

	// Expired: empty subscription (still with the outage contingency notes)
	_, expTok := mkUser("carol", 0, "2020-01-01T00:00:00Z")
	if rec := getSub(t, r, "/sub/"+expTok, "v2rayNG/1.8"); rec.Code != http.StatusOK {
		t.Fatalf("expired: %d", rec.Code)
	} else if raw, err := base64.StdEncoding.DecodeString(rec.Body.String()); err != nil ||
		strings.Contains(string(raw), "vless://") || !strings.Contains(string(raw), "公告订阅") {
		t.Fatalf("expired should be nodeless sub with notes: %v %q", err, string(raw))
	}

	// Unlimited quota (0) with usage: nodes are served as usual
	freeID, freeTok := mkUser("dave", 0, "")
	fid := freeID
	if err := db.Create(&storage.TrafficLog{UserID: &fid, RxBytes: 1, TxBytes: 2, RecordedAt: time.Now()}).Error; err != nil {
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

// TestSubResetCycleWindow encodes the P1-4 semantics: for a day-cycle user, traffic
// outside the window does not count toward the over-quota verdict; only over-quota
// traffic inside the window blocks the subscription.
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

	// 200 last month against a 100 quota: outside the day window -> nodes served as
	// usual, and the usage header only counts the window
	uid := uint(u["id"].(float64))
	if err := db.Create(&storage.TrafficLog{
		UserID: &uid, RxBytes: 200,
		RecordedAt: time.Now().AddDate(0, -1, 0),
	}).Error; err != nil {
		t.Fatal(err)
	}
	r1 := getSub(t, r, "/sub/"+tok, "v2rayNG/1.8")
	if r1.Code != http.StatusOK || r1.Body.String() == "" {
		t.Fatalf("窗口外流量不应封订阅: %d %q", r1.Code, r1.Body)
	}

	// Add 60 today: 60 < 100 inside the window -> nodes still served
	uid2 := uint(u["id"].(float64))
	if err := db.Create(&storage.TrafficLog{
		UserID: &uid2, RxBytes: 60, RecordedAt: time.Now(),
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

	// Today's total reaches 100 -> blocked
	uid3 := uint(u["id"].(float64))
	if err := db.Create(&storage.TrafficLog{
		UserID: &uid3, RxBytes: 40, RecordedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	r3 := getSub(t, r, "/sub/"+tok, "v2rayNG/1.8")
	if raw3, err := base64.StdEncoding.DecodeString(r3.Body.String()); err != nil ||
		strings.Contains(string(raw3), "vless://") || !strings.Contains(string(raw3), "公告订阅") {
		t.Fatalf("窗口内超限应封订阅（注释仍在）: %v %q", err, r3.Body)
	}
}

// TestSubscriptionRateLimit covers subscription endpoint rate limiting (Security
// Design §4): the token layer allows 30/minute to stop single-token brute force;
// the miss layer locks an IP for 15 minutes after 20 consecutive unknown/disabled
// tokens, blocking enumeration that rotates a fresh token each time; 429 hits are
// logged with a redacted token prefix.
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

	// Token layer: the same token passes within 30 requests per window, and the 31st
	// gets 429 plus a log line.
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
	// Isolation per token: alice exhausting her budget does not affect bob.
	if rec := getSub(t, r, "/sub/"+bob, "v2rayNG/1.8"); rec.Code != http.StatusOK {
		t.Fatalf("bob 应不受 alice 耗尽影响: %d", rec.Code)
	}

	// Miss layer: after 20 consecutive unknown tokens (each different) from the same
	// IP, the lock engages -- the 21st unknown token gets 429, and valid tokens from
	// that IP are locked too.
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

// TestSubscriptionHysteria2 covers the full hy2 node chain (hy2 batch): node
// creation validation (400 without a password) -> subscription link (hysteria2://)
// -> clash (type: hysteria2) -> singbox (outbounds array).
func TestSubscriptionHysteria2(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Node creation: a missing password is rejected (an explicit panel error, never silent).
	if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "bad", "address": "a", "port": 443, "protocol": "hysteria2", "config": `{"sni":"s.com"}`,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("hy2 missing password: %d %s", rec.Code, rec.Body)
	}
	// Node creation: full-field hy2.
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

	// User + subscription.
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "hyuser"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	token := u["sub_token"].(string)
	subPath := "/sub/" + token

	// v2ray target: a hysteria2:// link (all fields asserted).
	raw, err := base64.StdEncoding.DecodeString(getSub(t, r, subPath, "NekoBox/1.0").Body.String())
	if err != nil {
		t.Fatalf("body not base64: %v", err)
	}
	wantLink := "hysteria2://pw@hy.example.com:443?down=200&obfs=salamander&obfs-password=op&sni=s.com&up=100"
	if n := strings.Count(string(raw), wantLink); n != 1 {
		t.Fatalf("expected 1 hy2 link, got %d: %q", n, string(raw))
	}

	// clash target: the type: hysteria2 entry and field names.
	body := getSub(t, r, subPath+"?target=clash", "curl/8.0").Body.String()
	for _, want := range []string{"type: hysteria2", "password: pw", "obfs: salamander", "obfs-password: op", "up: 100", "down: 200", "sni: s.com"} {
		if !strings.Contains(body, want) {
			t.Fatalf("clash body missing %q:\n%s", want, body)
		}
	}

	// singbox target: the outbounds array carries a hysteria2 outbound.
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

	// Routing synthesis reports an honest error for hy2 (xray syntax does not fit a YAML core).
	if rec = doJSON(t, r, "GET", "/api/nodes/1/routing-config", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("routing render on hy2: %d %s", rec.Code, rec.Body)
	}
}

// TestSubscriptionSingBoxMixed covers mixed-fleet singbox subscriptions (SB batch):
// when xray-family and hy2 nodes coexist, target=singbox yields the full outbound
// list instead of failing the whole pack.
func TestSubscriptionSingBoxMixed(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	nodes := []map[string]any{
		{"name": "vl", "address": "vl.example.com", "port": 443, "protocol": "vless",
			"config": `{"uuid":"u-1","tls":true,"sni":"s.com","net":"ws","host":"cdn.example.com","path":"/ws"}`},
		{"name": "tj", "address": "tj.example.com", "port": 443, "protocol": "trojan",
			"config": `{"password":"p2","sni":"s.com"}`},
		{"name": "ss", "address": "ss.example.com", "port": 8388, "protocol": "shadowsocks",
			"config": `{"method":"aes-256-gcm","password":"p3"}`},
		{"name": "hy", "address": "hy.example.com", "port": 443, "protocol": "hysteria2",
			"config": `{"password":"p4","sni":"s.com"}`},
	}
	for i, n := range nodes {
		if rec := doJSON(t, r, "POST", "/api/nodes", n); rec.Code != http.StatusCreated {
			t.Fatalf("create node %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	// Entering the subscription surface: the entry role (subscriptions only serve
	// active entry/both nodes).
	if err := db.Model(&storage.Node{}).Where("name IN ?", []string{"vl", "tj", "ss", "hy"}).
		Update("role", "entry").Error; err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "sbuser"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)

	sb := getSub(t, r, "/sub/"+u["sub_token"].(string)+"?target=singbox", "curl/8.0")
	if sb.Code != http.StatusOK {
		t.Fatalf("singbox target: %d %s", sb.Code, sb.Body)
	}
	var obs []map[string]any
	if err := json.Unmarshal(sb.Body.Bytes(), &obs); err != nil {
		t.Fatalf("singbox not json array: %v\n%s", err, sb.Body)
	}
	if len(obs) != len(nodes) {
		t.Fatalf("outbounds = %d, want %d:\n%s", len(obs), len(nodes), sb.Body)
	}
	wantTypes := map[int]any{0: "vless", 1: "trojan", 2: "shadowsocks", 3: "hysteria2"}
	for i, want := range wantTypes {
		if obs[i]["type"] != want {
			t.Fatalf("outbound[%d] type = %v, want %v", i, obs[i]["type"], want)
		}
	}
	// vless ws transport lands in the sing-box transport object.
	if tr, ok := obs[0]["transport"].(map[string]any); !ok || tr["type"] != "ws" || tr["path"] != "/ws" {
		t.Fatalf("vless transport = %v", obs[0]["transport"])
	}
	// trojan is always TLS.
	if tls, ok := obs[1]["tls"].(map[string]any); !ok || tls["enabled"] != true {
		t.Fatalf("trojan tls = %v", obs[1]["tls"])
	}
}

// TestSubscriptionVersionAndTime covers the unified subscription metadata
// (F-1108): the update time rides Last-Modified in HTTP date format and the
// version stamp rides X-Subscription-Version; the same body keeps the same
// version on refetch, and the custom subscription surface carries the same
// metadata (both deliver through one helper, no drift).
func TestSubscriptionVersionAndTime(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	panel := panelUserOf(t, r, "ver-admin", nil)
	token := panelUserOf(t, r, "ver-user", nil)

	node := storage.Node{Name: "ver-hk", Address: "v.example.com", Port: 443, Protocol: "vless",
		Config: `{"uuid":"u-9"}`, Enabled: true, Token: "ver-tok-1", Role: "entry", Status: "online"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}

	assertMeta := func(rec *httptest.ResponseRecorder) string {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("sub status = %d %s", rec.Code, rec.Body)
		}
		if _, err := http.ParseTime(rec.Header().Get("Last-Modified")); err != nil {
			t.Fatalf("last-modified not an HTTP date: %q", rec.Header().Get("Last-Modified"))
		}
		v := rec.Header().Get("X-Subscription-Version")
		if len(v) != 12 {
			t.Fatalf("version stamp = %q", v)
		}
		return v
	}

	rec := getSub(t, r, "/sub/"+token, "v2rayNG/1.8")
	v := assertMeta(rec)
	rec2 := getSub(t, r, "/sub/"+token, "v2rayNG/1.8")
	if got := assertMeta(rec2); got != v {
		t.Fatalf("same body must keep the version: %q vs %q", got, v)
	}

	// the custom subscription surface carries the same metadata
	rec = doJSONToken(t, r, panel, "POST", "/api/subscriptions", map[string]any{"name": "ver-sub"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create sub: %d %s", rec.Code, rec.Body)
	}
	var s storage.Subscription
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	rec = getSub(t, r, "/sub/x/"+s.Token, "v2rayNG/1.8")
	if got := assertMeta(rec); got != v {
		t.Fatalf("custom sub metadata must match: %q vs %q", got, v)
	}
}
