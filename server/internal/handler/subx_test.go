// End-to-end tests for self-built subscriptions (ferry-sub-filters): CRUD and
// guards (cross-user 404, cap 409, strict JSON 400, token reset), /sub/x/:token
// three-format UA negotiation golden assertions (v2ray base64 decoded and
// compared node by node), max_nodes truncation, disabled user/subscription
// anti-enumeration, rate limiting, plus the regression anchor that the main
// subscription /sub/:token is unaffected.
package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// panelUserOf creates a user and returns the subscription token (panel identity).
func panelUserOf(t *testing.T, r *gin.Engine, name string, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"username": name, "quota_bytes": int64(1 << 40)}
	for k, v := range extra {
		body[k] = v
	}
	rec := doJSON(t, r, "POST", "/api/users", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user %s: %d %s", name, rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	return u["sub_token"].(string)
}

// seedEntryNodes inserts three entry nodes directly (labels are not part of
// NodeInput, so direct insertion bypasses the API):
// hk-vless / hk-hy (Hong Kong) + us-vmess (US).
func seedEntryNodes(t *testing.T, db *gorm.DB) {
	t.Helper()
	nodes := []storage.Node{
		{Name: "hk-vless", Address: "a.example.com", Port: 443, Protocol: "vless",
			Config: `{"uuid":"u-1","tls":true,"sni":"s.com"}`, Enabled: true,
			Token: "sx-tok-vless", Role: "entry", Region: "香港", Labels: `["streaming"]`, Status: "online"},
		{Name: "hk-hy", Address: "b.example.com", Port: 443, Protocol: "hysteria2",
			Config: `{"password":"pw"}`, Enabled: true,
			Token: "sx-tok-hy", Role: "entry", Region: "香港", Labels: `["streaming"]`, Status: "online"},
		{Name: "us-vmess", Address: "c.example.com", Port: 443, Protocol: "vmess",
			Config: `{"uuid":"u-3"}`, Enabled: true,
			Token: "sx-tok-vmess", Role: "entry", Region: "美国", Labels: `["game"]`, Status: "online"},
	}
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// createSubX creates a self-built subscription via the panel identity and returns its token.
func createSubX(t *testing.T, r *gin.Engine, panelToken, name, filter, config string) string {
	t.Helper()
	body := map[string]any{"name": name}
	if filter != "" {
		body["filter"] = json.RawMessage(filter)
	}
	if config != "" {
		body["config"] = json.RawMessage(config)
	}
	rec := doJSONToken(t, r, panelToken, "POST", "/api/subscriptions", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create sub %s: %d %s", name, rec.Code, rec.Body)
	}
	var s storage.Subscription
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	return s.Token
}

// newRawJSON sends a raw JSON text request (panel identity), keeping the misspelled
// body verbatim instead of having map[string]any normalize it; used to test strict
// decoding (unknown fields give 400).
func newRawJSON(t *testing.T, r *gin.Engine, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// vmessHosts extracts the add host of each vmess:// payload from v2ray subscription text.
func vmessHosts(text string) []string {
	var hosts []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "vmess://") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "vmess://"))
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			if add, ok := m["add"].(string); ok {
				hosts = append(hosts, add)
			}
		}
	}
	return hosts
}

func containsHost(hosts []string, want string) bool {
	for _, h := range hosts {
		if h == want {
			return true
		}
	}
	return false
}

// TestSubXCRUDAndGuards covers the CRUD main chain and the guards: create/list/update/
// delete/token reset, strict JSON (unknown fields 400), required name, per-user cap 409,
// and disabled subscription 404.
func TestSubXCRUDAndGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	panel := panelUserOf(t, r, "sx-crud", nil)

	// Create: filter/config default to "{}", enabled defaults to true, token generated server-side.
	rec := doJSONToken(t, r, panel, "POST", "/api/subscriptions", map[string]any{"name": "香港专用"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var s storage.Subscription
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Token == "" || len(s.Token) < 20 || !s.Enabled || s.Filter != "{}" || s.Config != "{}" {
		t.Fatalf("created sub = %+v", s)
	}

	// List: the owner can see the token in plaintext.
	rec = doJSONToken(t, r, panel, "GET", "/api/subscriptions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var list []storage.Subscription
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Token != s.Token {
		t.Fatalf("list = %+v", list)
	}

	// Update: name/filter/enabled; with no fields it idempotently returns the current values.
	rec = doJSONToken(t, r, panel, "PATCH", "/api/subscriptions/1",
		map[string]any{"name": "改名", "filter": map[string]any{"regions": []string{"香港"}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSONToken(t, r, panel, "PATCH", "/api/subscriptions/1", map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("patch empty: %d %s", rec.Code, rec.Body)
	}

	// Strict mode: unknown filter/config/top-level fields, malformed JSON, and empty name all give 400.
	for _, tc := range []struct {
		name string
		body string
	}{
		{"unknown filter field", `{"name":"x","filter":{"regoin":["香港"]}}`},
		{"unknown config field", `{"name":"x","config":{"max_node":3}}`},
		{"bad filter json", `{"name":"x","filter":"{bad"}`},
		{"empty name", `{"name":""}`},
		{"unknown top field", `{"nam":"x"}`},
	} {
		if req := newRawJSON(t, r, "POST", "/api/subscriptions", tc.body, panel); req.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", tc.name, req.Code, req.Body)
		}
	}

	// Per-user cap: after filling up to 10, the 11th gives 409.
	for i := 0; i < 9; i++ {
		rec := doJSONToken(t, r, panel, "POST", "/api/subscriptions", map[string]any{"name": "批量"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("fill %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if rec := doJSONToken(t, r, panel, "POST", "/api/subscriptions", map[string]any{"name": "超限"}); rec.Code != http.StatusConflict {
		t.Fatalf("limit: %d %s", rec.Code, rec.Body)
	}

	// After disabling, delivery returns 404 (anti-enumeration, same policy as an unknown token).
	if rec := doJSONToken(t, r, panel, "PATCH", "/api/subscriptions/1", map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	if rec := getSub(t, r, "/sub/x/"+s.Token, "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled sub: %d", rec.Code)
	}
	_ = doJSONToken(t, r, panel, "PATCH", "/api/subscriptions/1", map[string]any{"enabled": true})

	// Reset token: the old token gives 404, the new one delivers.
	rec = doJSONToken(t, r, panel, "POST", "/api/subscriptions/1/reset-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	var renewed storage.Subscription
	_ = json.Unmarshal(rec.Body.Bytes(), &renewed)
	if renewed.Token == "" || renewed.Token == s.Token {
		t.Fatalf("reset token not renewed: %q -> %q", s.Token, renewed.Token)
	}
	if rec := getSub(t, r, "/sub/x/"+s.Token, "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("old token after reset: %d", rec.Code)
	}
	if rec := getSub(t, r, "/sub/x/"+renewed.Token, "v2rayNG/1.8"); rec.Code != http.StatusOK {
		t.Fatalf("new token after reset: %d %s", rec.Code, rec.Body)
	}

	// Delete: fetching again after the delete gives 404, and the list is back to 9.
	if rec := doJSONToken(t, r, panel, "DELETE", "/api/subscriptions/1", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := getSub(t, r, "/sub/x/"+renewed.Token, "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted sub: %d", rec.Code)
	}
	rec = doJSONToken(t, r, panel, "GET", "/api/subscriptions", nil)
	var after []storage.Subscription
	_ = json.Unmarshal(rec.Body.Bytes(), &after)
	if len(after) != 9 {
		t.Fatalf("after delete list = %d, want 9", len(after))
	}
}

// TestSubXCrossUser404 cross-user access: patch/delete/reset on another user's
// subscription id always give 404 (existence is not leaked).
func TestSubXCrossUser404(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	alice := panelUserOf(t, r, "sx-alice", nil)
	bob := panelUserOf(t, r, "sx-bob", nil)
	subToken := createSubX(t, r, alice, "alice的", "", "")

	if rec := doJSONToken(t, r, bob, "PATCH", "/api/subscriptions/1", map[string]any{"name": "抢改"}); rec.Code != http.StatusNotFound {
		t.Fatalf("cross patch: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSONToken(t, r, bob, "DELETE", "/api/subscriptions/1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross delete: %d", rec.Code)
	}
	if rec := doJSONToken(t, r, bob, "POST", "/api/subscriptions/1/reset-token", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross reset: %d", rec.Code)
	}
	// After the cross-user failures the originals are untouched: name unchanged, token not swapped.
	var list []storage.Subscription
	rec := doJSONToken(t, r, alice, "GET", "/api/subscriptions", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Name != "alice的" || list[0].Token != subToken {
		t.Fatalf("alice sub after cross ops = %+v", list)
	}
}

// TestSubXDeliveryFilterAndConfig end-to-end golden assertions: filter (region+protocol)
// matching, config (remark_template/include_rtt/max_nodes) in effect, three-format UA
// negotiation output comparison, and the main-subscription regression anchor (all three
// nodes unaffected).
func TestSubXDeliveryFilterAndConfig(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedEntryNodes(t, db)
	panel := panelUserOf(t, r, "sx-deliver", map[string]any{
		"expires_at": "2030-01-01T00:00:00Z",
	})

	// Only Hong Kong + hysteria2; remark template with RTT off; max_nodes=3.
	subToken := createSubX(t, r, panel, "香港 hy2 专用",
		`{"regions":["香港"],"protocols":["hysteria2"]}`,
		`{"remark_template":"{region}/{name}/{rtt}ms","include_rtt":false,"sort":"name","max_nodes":3}`)
	path := "/sub/x/" + subToken

	// v2ray (default UA): base64-decode and compare node by node — only hk-hy is present.
	rec := getSub(t, r, path, "v2rayNG/1.8")
	if rec.Code != http.StatusOK {
		t.Fatalf("v2ray: %d %s", rec.Code, rec.Body)
	}
	raw, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil {
		t.Fatalf("not base64: %v", err)
	}
	text := string(raw)
	if n := strings.Count(text, "hysteria2://"); n != 1 {
		t.Fatalf("hysteria2 links = %d, want 1:\n%s", n, text)
	}
	if !strings.Contains(text, "b.example.com") || !strings.Contains(text, "hk-hy") {
		t.Fatalf("hk-hy missing:\n%s", text)
	}
	for _, leak := range []string{"a.example.com", "c.example.com", "vless://", "vmess://", "hk-vless", "us-vmess"} {
		if strings.Contains(text, leak) {
			t.Fatalf("filtered node leaked %q:\n%s", leak, text)
		}
	}
	// Remark template: include_rtt=false → {rtt} is emptied; the template renders as
	// {region}/{name}/{rtt}ms, so protocol-internal fragments pass url.PathEscape and
	// "/" plus non-ASCII region text get escaped.
	if !strings.Contains(text, "hk-hy%2Fms") {
		t.Fatalf("remark template not applied:\n%s", text)
	}
	if !strings.Contains(text, "%E9%A6%99%E6%B8%AF") { // percent-encoded region text
		t.Fatalf("region placeholder not replaced:\n%s", text)
	}
	// Guard headers match the main subscription.
	if info := rec.Header().Get("Subscription-Userinfo"); !strings.Contains(info, "upload=") {
		t.Fatalf("userinfo header = %q", info)
	}
	if rec.Header().Get("Profile-Title") != "sx-deliver" || rec.Header().Get("Profile-Update-Interval") == "" {
		t.Fatalf("profile headers = %v", rec.Header())
	}

	// clash UA: the YAML only has the hysteria2 proxy.
	body := getSub(t, r, path, "clash-verge/2.0").Body.String()
	if !strings.Contains(body, "type: hysteria2") || strings.Contains(body, "type: vless") || strings.Contains(body, "vmess") {
		t.Fatalf("clash filter:\n%s", body)
	}

	// Explicit singbox target: the JSON only has the hk-hy outbound.
	if b := getSub(t, r, path+"?target=singbox", "curl/8.0").Body.String(); !strings.Contains(b, "hk-hy") || strings.Contains(b, "us-vmess") {
		t.Fatalf("singbox filter:\n%s", b)
	}

	// sing-box/Stash UA: the JSON only has the hk-hy outbound (SHOP-2, UA negotiation serves singbox).
	for _, ua := range []string{"sing-box/1.8.0", "Stash/2.0"} {
		if b := getSub(t, r, path, ua).Body.String(); !strings.Contains(b, "hk-hy") || strings.Contains(b, "us-vmess") {
			t.Fatalf("singbox UA %s filter:\n%s", ua, b)
		}
	}
	if rec := getSub(t, r, path+"?target=bad", "curl/8.0"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad target: %d", rec.Code)
	}

	// Regression anchor: the main subscription /sub/:token still has all three nodes (behavior unaffected by this feature).
	mainRec := getSub(t, r, "/sub/"+panel, "v2rayNG/1.8")
	if mainRec.Code != http.StatusOK {
		t.Fatalf("main sub: %d", mainRec.Code)
	}
	mainRaw, _ := base64.StdEncoding.DecodeString(mainRec.Body.String())
	mainText := string(mainRaw)
	// Assert plaintext links by host (vless/hysteria2 hosts are in plaintext, the vmess host is in the base64 payload).
	for _, want := range []string{"a.example.com", "b.example.com", "vless://", "hysteria2://"} {
		if !strings.Contains(mainText, want) {
			t.Fatalf("main sub missing %q:\n%s", want, mainText)
		}
	}
	if !containsHost(vmessHosts(mainText), "c.example.com") {
		t.Fatalf("main sub missing vmess us-vmess:\n%s", mainText)
	}
}

// TestSubXMaxNodesTruncate max_nodes truncation: three Hong Kong entry nodes, cap 2,
// sorted by name → exactly the first two (truncated in name order).
func TestSubXMaxNodesTruncate(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	for _, n := range []storage.Node{
		{Name: "n2", Address: "n2.example.com", Port: 443, Protocol: "trojan", Config: `{"password":"p2"}`,
			Token: "sx-tok-n2", Enabled: true, Role: "entry", Region: "香港", Status: "online"},
		{Name: "n1", Address: "n1.example.com", Port: 443, Protocol: "trojan", Config: `{"password":"p1"}`,
			Token: "sx-tok-n1", Enabled: true, Role: "entry", Region: "香港", Status: "online"},
		{Name: "n3", Address: "n3.example.com", Port: 443, Protocol: "trojan", Config: `{"password":"p3"}`,
			Token: "sx-tok-n3", Enabled: true, Role: "entry", Region: "香港", Status: "online"},
	} {
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	panel := panelUserOf(t, r, "sx-trunc", nil)
	subToken := createSubX(t, r, panel, "截断", `{"regions":["香港"]}`, `{"sort":"name","max_nodes":2}`)

	raw, _ := base64.StdEncoding.DecodeString(getSub(t, r, "/sub/x/"+subToken, "v2rayNG/1.8").Body.String())
	text := string(raw)
	if n := strings.Count(text, "trojan://"); n != 2 {
		t.Fatalf("links = %d, want 2:\n%s", n, text)
	}
	if !strings.Contains(text, "n1") || !strings.Contains(text, "n2") || strings.Contains(text, "n3.example.com") {
		t.Fatalf("truncate picked wrong nodes:\n%s", text)
	}
}

// TestSubXUserStates user-state branches: unknown token 404, disabled user empty list
// (returned normally, not 403), expired user empty list, orphaned subscription (user row
// deleted) 404.
func TestSubXUserStates(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedEntryNodes(t, db)

	// Unknown token: 404.
	if rec := getSub(t, r, "/sub/x/nope", "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", rec.Code)
	}

	// Disabled user: 200 with an empty list as usual (not 403), usage headers still present.
	panel := panelUserOf(t, r, "sx-disabled", nil)
	subToken := createSubX(t, r, panel, "停用用户", "", "")
	if rec := doJSON(t, r, "PUT", "/api/users/1", map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("disable user: %d %s", rec.Code, rec.Body)
	}
	rec := getSub(t, r, "/sub/x/"+subToken, "v2rayNG/1.8")
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled user sub: %d", rec.Code)
	}
	if raw, _ := base64.StdEncoding.DecodeString(rec.Body.String()); strings.Contains(string(raw), "example.com") {
		t.Fatalf("disabled user got nodes:\n%s", string(raw))
	}
	if rec.Header().Get("Subscription-Userinfo") == "" {
		t.Fatal("userinfo header missing for disabled user")
	}

	// Expired user: same policy, empty list.
	panel2 := panelUserOf(t, r, "sx-expired", map[string]any{"expires_at": "2020-01-01T00:00:00Z"})
	subToken2 := createSubX(t, r, panel2, "到期用户", `{"regions":["香港"]}`, "")
	rec = getSub(t, r, "/sub/x/"+subToken2, "v2rayNG/1.8")
	if rec.Code != http.StatusOK {
		t.Fatalf("expired user sub: %d", rec.Code)
	}
	if raw, _ := base64.StdEncoding.DecodeString(rec.Body.String()); strings.Contains(string(raw), "example.com") {
		t.Fatalf("expired user got nodes:\n%s", string(raw))
	}

	// Orphaned subscription (user row deleted): treated as revoked, 404.
	panel3 := panelUserOf(t, r, "sx-gone", nil)
	subToken3 := createSubX(t, r, panel3, "悬挂", "", "")
	var u3 storage.User
	db.Where("username = ?", "sx-gone").First(&u3)
	if err := db.Delete(&u3).Error; err != nil {
		t.Fatal(err)
	}
	if rec := getSub(t, r, "/sub/x/"+subToken3, "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("orphan sub: %d", rec.Code)
	}
}

// TestSubXRateLimit rate limiting (shares the main subscription's limiter): the same
// token gets 30/minute allowed and the 31st gives 429; the same IP locks at 429 after
// consecutive unknown tokens.
func TestSubXRateLimit(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	panel := panelUserOf(t, r, "sx-ratelimit", nil)
	subToken := createSubX(t, r, panel, "限流", "", "")

	for i := 0; i < 30; i++ {
		if rec := getSub(t, r, "/sub/x/"+subToken, "v2rayNG/1.8"); rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次应放行: %d", i+1, rec.Code)
		}
	}
	if rec := getSub(t, r, "/sub/x/"+subToken, "v2rayNG/1.8"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("第 31 次应 429: %d", rec.Code)
	}
	// Unknown tokens count failures per IP: after consecutive hits it locks, 429 from the 21st.
	for i := 0; i < 20; i++ {
		if rec := getSub(t, r, "/sub/x/enum-x-probe", "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
			t.Fatalf("未知令牌第 %d 次应 404: %d", i+1, rec.Code)
		}
	}
	if rec := getSub(t, r, "/sub/x/enum-x-lock", "v2rayNG/1.8"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("枚举锁定后应 429: %d", rec.Code)
	}
}
