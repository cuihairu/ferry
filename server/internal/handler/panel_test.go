package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestPanelOrders(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "erin", map[string]any{
		"grant_type": "extend_days", "grant_value": 30,
	}, 1)
	var u storage.User
	if err := db.First(&u, userID).Error; err != nil {
		t.Fatal(err)
	}
	// 真实兑换一笔，产生 order + grant
	if rec := doPanel(t, r, u.SubToken, "POST", "/api/panel/redeem", map[string]any{"code": codes[0]}); rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}

	// 订单中心：倒序，发放记录归并进单
	rec := doPanel(t, r, u.SubToken, "GET", "/api/panel/orders", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("orders: %d %s", rec.Code, rec.Body)
	}
	var list []struct {
		OrderNo  string `json:"order_no"`
		Provider string `json:"provider"`
		Status   string `json:"status"`
		Grants   []struct {
			GrantType  string `json:"grant_type"`
			GrantValue int64  `json:"grant_value"`
		} `json:"grants"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].Provider != "card" || list[0].Status != "paid" {
		t.Fatalf("orders = %+v", list)
	}
	if len(list[0].Grants) != 1 || list[0].Grants[0].GrantType != "extend_days" || list[0].Grants[0].GrantValue != 30 {
		t.Fatalf("grants = %+v", list[0].Grants)
	}

	// 隔离：别人的单看不见
	otherID, _ := redeemCreate(t, r, "frank", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 1)
	var ou storage.User
	if err := db.First(&ou, otherID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.PaymentOrder{OrderNo: "card-9-9", UserID: ou.ID, Provider: "card", Status: "paid"}).Error; err != nil {
		t.Fatal(err)
	}
	rec = doPanel(t, r, u.SubToken, "GET", "/api/panel/orders", nil)
	var list2 []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &list2)
	if len(list2) != 1 {
		t.Fatalf("leak other order: %+v", list2)
	}

	// 非法 limit 与无凭据
	if rec := doPanel(t, r, u.SubToken, "GET", "/api/panel/orders?limit=0", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: %d", rec.Code)
	}
	if rec := doPanel(t, r, "", "GET", "/api/panel/orders", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
}

// TestPanelSavings 覆盖用户侧「已为你省下」（SAVE-8）：节点节省按该用户
// 当月记账流量占比折算，跨节点汇总；上月窗口外不计，无用户记账不摊派。
func TestPanelSavings(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	token := panelToken(t, r, "saver")
	panelToken(t, r, "other")

	uid := func(name string) uint {
		var u storage.User
		if err := db.Where("username = ?", name).First(&u).Error; err != nil {
			t.Fatalf("find user %s: %v", name, err)
		}
		return u.ID
	}
	saverID, otherID := uid("saver"), uid("other")

	for _, name := range []string{"n1", "n2", "n3"} {
		if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
		}); rec.Code != http.StatusCreated {
			t.Fatalf("create node %s: %d %s", name, rec.Code, rec.Body)
		}
	}

	now := time.Now().UTC()
	seedSave := []storage.SaveStat{
		// n1 本月：直连 1000 + 拦截 100；该用户占记账流量一半 → 折算 500/50
		{NodeID: 1, Day: now.Format("2006-01-02"), DirectBytes: 1000, BlockedBytes: 100},
		// n2 本月：直连 400；记账全为该用户 → 全额 400
		{NodeID: 2, Day: now.Format("2006-01-02"), DirectBytes: 400},
		// n3 本月：直连 999；当月无任何用户记账 → 不摊派
		{NodeID: 3, Day: now.Format("2006-01-02"), DirectBytes: 999},
		// n1 上月：窗口外不计
		{NodeID: 1, Day: now.AddDate(0, 0, -40).Format("2006-01-02"), DirectBytes: 88888},
	}
	if err := db.Create(&seedSave).Error; err != nil {
		t.Fatalf("seed save_stats: %v", err)
	}
	n1, n2 := uint(1), uint(2)
	seedTraffic := []storage.TrafficLog{
		{UserID: saverID, NodeID: &n1, RxBytes: 600, RecordedAt: now},
		// 他人同节点流量摊薄占比：saver 占 n1 一半
		{UserID: otherID, NodeID: &n1, RxBytes: 600, RecordedAt: now},
		{UserID: saverID, NodeID: &n2, RxBytes: 100, RecordedAt: now},
		// 上月流量不进窗口
		{UserID: saverID, NodeID: &n1, RxBytes: 5000, RecordedAt: now.AddDate(0, 0, -40)},
	}
	if err := db.Create(&seedTraffic).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	rec := doPanel(t, r, token, "GET", "/api/panel/savings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("savings: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Window        string `json:"window"`
		DirectBytes   int64  `json:"direct_bytes"`
		CacheHitBytes int64  `json:"cache_hit_bytes"`
		BlockedBytes  int64  `json:"blocked_bytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.DirectBytes != 900 || out.BlockedBytes != 50 || out.CacheHitBytes != 0 {
		t.Fatalf("unexpected savings: %+v", out)
	}
	if !strings.HasPrefix(out.Window, now.Format("2006-01")) {
		t.Fatalf("window not current month: %s", out.Window)
	}

	// 无凭据 404（令牌视同吊销口径）
	if rec := doPanel(t, r, "", "GET", "/api/panel/savings", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
}
