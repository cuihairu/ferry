// 自建订阅端到端测试（ferry-sub-filters）：CRUD 与护栏（越权 404、上限 409、
// 严格 JSON 400、重置 token）、/sub/x/:token 三格式 UA 协商黄金断言（v2ray
// base64 解码后逐节点比对）、max_nodes 截断、停用用户/订阅防枚举、限流，
// 以及主订阅 /sub/:token 不受影响的回归锚。
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

// panelUserOf 建用户并返回订阅令牌（面板身份）。
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

// seedEntryNodes 直插三个入口节点（labels 不在 NodeInput，直插绕过 API）：
// hk-vless / hk-hy（香港）+ us-vmess（美国）。
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

// createSubX 经面板身份建一个自建订阅，返回其 token。
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

// newRawJSON 发原始 JSON 文本请求（面板身份），保留拼写错误原文不被
// map[string]any 修正，专测严格解码（未知字段 400）。
func newRawJSON(t *testing.T, r *gin.Engine, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// vmessHosts 从 v2ray 订阅文本里解出各 vmess:// 载荷的 add 主机。
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

// TestSubXCRUDAndGuards 覆盖 CRUD 主链与护栏：创建/列表/更新/删除/重置 token、
// 严格 JSON（未知字段 400）、name 必填、每用户上限 409、禁用订阅 404。
func TestSubXCRUDAndGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	panel := panelUserOf(t, r, "sx-crud", nil)

	// 创建：filter/config 缺省存 "{}"，enabled 缺省 true，token 服务端生成。
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

	// 列表：本人可见 token 明文。
	rec = doJSONToken(t, r, panel, "GET", "/api/subscriptions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var list []storage.Subscription
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Token != s.Token {
		t.Fatalf("list = %+v", list)
	}

	// 更新：name/filter/enabled；无字段时幂等返回当前值。
	rec = doJSONToken(t, r, panel, "PATCH", "/api/subscriptions/1",
		map[string]any{"name": "改名", "filter": map[string]any{"regions": []string{"香港"}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSONToken(t, r, panel, "PATCH", "/api/subscriptions/1", map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("patch empty: %d %s", rec.Code, rec.Body)
	}

	// 严格模式：未知 filter/config/顶层字段、坏 JSON、空 name 均 400。
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

	// 每用户上限：补到 10 个后第 11 个 409。
	for i := 0; i < 9; i++ {
		rec := doJSONToken(t, r, panel, "POST", "/api/subscriptions", map[string]any{"name": "批量"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("fill %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if rec := doJSONToken(t, r, panel, "POST", "/api/subscriptions", map[string]any{"name": "超限"}); rec.Code != http.StatusConflict {
		t.Fatalf("limit: %d %s", rec.Code, rec.Body)
	}

	// 禁用订阅后下发 404（防枚举，同未知令牌口径）。
	if rec := doJSONToken(t, r, panel, "PATCH", "/api/subscriptions/1", map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	if rec := getSub(t, r, "/sub/x/"+s.Token, "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled sub: %d", rec.Code)
	}
	_ = doJSONToken(t, r, panel, "PATCH", "/api/subscriptions/1", map[string]any{"enabled": true})

	// 重置 token：旧令牌 404、新令牌可下发。
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

	// 删除：删后再查 404，列表回到 9。
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

// TestSubXCrossUser404 越权：他人订阅 id 的改/删/重置一律 404（不泄露存在性）。
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
	// 越权失败后原值未动：名字未改、令牌未换。
	var list []storage.Subscription
	rec := doJSONToken(t, r, alice, "GET", "/api/subscriptions", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Name != "alice的" || list[0].Token != subToken {
		t.Fatalf("alice sub after cross ops = %+v", list)
	}
}

// TestSubXDeliveryFilterAndConfig 端到端黄金断言：filter（region+protocol）
// 命中、config（remark_template/include_rtt/max_nodes）生效、三格式 UA 协商
// 输出比对、主订阅回归锚（全量三节点不受影响）。
func TestSubXDeliveryFilterAndConfig(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedEntryNodes(t, db)
	panel := panelUserOf(t, r, "sx-deliver", map[string]any{
		"expires_at": "2030-01-01T00:00:00Z",
	})

	// 只含香港 + hysteria2；备注模板关 RTT；max_nodes=3。
	subToken := createSubX(t, r, panel, "香港 hy2 专用",
		`{"regions":["香港"],"protocols":["hysteria2"]}`,
		`{"remark_template":"{region}/{name}/{rtt}ms","include_rtt":false,"sort":"name","max_nodes":3}`)
	path := "/sub/x/" + subToken

	// v2ray（默认 UA）：base64 解码逐节点比对——只有 hk-hy 一条。
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
	// 备注模板：include_rtt=false → {rtt} 置空，模板 "香港/hk-hy/ms"；
	// 协议内片段经 url.PathEscape，"/" 与中文被转义。
	if !strings.Contains(text, "hk-hy%2Fms") {
		t.Fatalf("remark template not applied:\n%s", text)
	}
	if !strings.Contains(text, "%E9%A6%99%E6%B8%AF") { // 香港
		t.Fatalf("region placeholder not replaced:\n%s", text)
	}
	// 护栏头与主订阅一致。
	if info := rec.Header().Get("Subscription-Userinfo"); !strings.Contains(info, "upload=") {
		t.Fatalf("userinfo header = %q", info)
	}
	if rec.Header().Get("Profile-Title") != "sx-deliver" || rec.Header().Get("Profile-Update-Interval") == "" {
		t.Fatalf("profile headers = %v", rec.Header())
	}

	// clash UA：YAML 只含 hysteria2 代理。
	body := getSub(t, r, path, "clash-verge/2.0").Body.String()
	if !strings.Contains(body, "type: hysteria2") || strings.Contains(body, "type: vless") || strings.Contains(body, "vmess") {
		t.Fatalf("clash filter:\n%s", body)
	}

	// singbox 显式 target：JSON 只含 hk-hy outbound。
	if b := getSub(t, r, path+"?target=singbox", "curl/8.0").Body.String(); !strings.Contains(b, "hk-hy") || strings.Contains(b, "us-vmess") {
		t.Fatalf("singbox filter:\n%s", b)
	}
	if rec := getSub(t, r, path+"?target=bad", "curl/8.0"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad target: %d", rec.Code)
	}

	// 回归锚：主订阅 /sub/:token 仍全量三节点（行为不受本特性影响）。
	mainRec := getSub(t, r, "/sub/"+panel, "v2rayNG/1.8")
	if mainRec.Code != http.StatusOK {
		t.Fatalf("main sub: %d", mainRec.Code)
	}
	mainRaw, _ := base64.StdEncoding.DecodeString(mainRec.Body.String())
	mainText := string(mainRaw)
	// 明文链接按主机断言（vless/hysteria2 主机在明文，vmess 主机在 base64 载荷）。
	for _, want := range []string{"a.example.com", "b.example.com", "vless://", "hysteria2://"} {
		if !strings.Contains(mainText, want) {
			t.Fatalf("main sub missing %q:\n%s", want, mainText)
		}
	}
	if !containsHost(vmessHosts(mainText), "c.example.com") {
		t.Fatalf("main sub missing vmess us-vmess:\n%s", mainText)
	}
}

// TestSubXMaxNodesTruncate max_nodes 截断：三个香港入口、上限 2、name 序
// → 恰好前两个（按名字序截断）。
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

// TestSubXUserStates 用户态分支：未知令牌 404、停用用户空列表（照常返回非
// 403）、到期用户空列表、订阅悬挂（用户行已删）404。
func TestSubXUserStates(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedEntryNodes(t, db)

	// 未知令牌 404。
	if rec := getSub(t, r, "/sub/x/nope", "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", rec.Code)
	}

	// 停用用户：照常 200 空列表（非 403），用量头仍在。
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

	// 到期用户：空列表同口径。
	panel2 := panelUserOf(t, r, "sx-expired", map[string]any{"expires_at": "2020-01-01T00:00:00Z"})
	subToken2 := createSubX(t, r, panel2, "到期用户", `{"regions":["香港"]}`, "")
	rec = getSub(t, r, "/sub/x/"+subToken2, "v2rayNG/1.8")
	if rec.Code != http.StatusOK {
		t.Fatalf("expired user sub: %d", rec.Code)
	}
	if raw, _ := base64.StdEncoding.DecodeString(rec.Body.String()); strings.Contains(string(raw), "example.com") {
		t.Fatalf("expired user got nodes:\n%s", string(raw))
	}

	// 订阅悬挂（用户行已删）：视同吊销 404。
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

// TestSubXRateLimit 限流（与主订阅共用同一限流器口径）：同 token 30 次/分钟
// 放行、31 次 429；同 IP 连续未知令牌后锁定 429。
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
	// 未知令牌按 IP 记失败：连续命中后锁定，第 21 个起 429。
	for i := 0; i < 20; i++ {
		if rec := getSub(t, r, "/sub/x/enum-x-probe", "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
			t.Fatalf("未知令牌第 %d 次应 404: %d", i+1, rec.Code)
		}
	}
	if rec := getSub(t, r, "/sub/x/enum-x-lock", "v2rayNG/1.8"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("枚举锁定后应 429: %d", rec.Code)
	}
}
