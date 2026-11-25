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

// doPanel sends a request with the subscription token as identity (PAY-7): an
// empty token means no credentials are sent.
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

// panelToken creates a user and returns its subscription token.
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

	// Token matches: identity and usage come back per the stated rules
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

	// No credentials / unknown token: 404 (anti-enumeration, the same stance as /sub)
	if rec := doPanel(t, r, "", "GET", "/api/panel/me", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := doPanel(t, r, "not-a-token", "GET", "/api/panel/me", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", rec.Code)
	}

	// Disabled user: the token counts as revoked, 404
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

	// Redemption: identity comes from the token, and the payload carries only the
	// code (user_id cannot be passed)
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

	// Reusing the same code: the unified failure message; the X-Ferry-Token header
	// also works as a credential
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

	// No credentials: 404, never reaching the redemption logic
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
	// Redeem for real once, producing an order + grant
	if rec := doPanel(t, r, u.SubToken, "POST", "/api/panel/redeem", map[string]any{"code": codes[0]}); rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}

	// Order center: reverse order, grant records merged into the order
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

	// Isolation: someone else's order is not visible
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

	// Illegal limit and no credentials
	if rec := doPanel(t, r, u.SubToken, "GET", "/api/panel/orders?limit=0", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: %d", rec.Code)
	}
	if rec := doPanel(t, r, "", "GET", "/api/panel/orders", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
}

// TestPanelSavings covers the user-side "saved for you" (SAVE-8): node savings
// are prorated by the user's share of recorded traffic for the month and summed
// across nodes; last month falls outside the window, and no allocation when a
// node has no recorded user traffic.
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
		// n1 this month: 1000 direct + 100 blocked; this user is half the recorded
		// traffic -> prorated 500/50
		{NodeID: 1, Day: now.Format("2006-01-02"), DirectBytes: 1000, BlockedBytes: 100},
		// n2 this month: 400 direct; all recorded traffic is this user's -> the full
		// 400
		{NodeID: 2, Day: now.Format("2006-01-02"), DirectBytes: 400},
		// n3 this month: 999 direct; no user traffic recorded this month -> no
		// allocation
		{NodeID: 3, Day: now.Format("2006-01-02"), DirectBytes: 999},
		// n1 last month: outside the window, not counted
		{NodeID: 1, Day: now.AddDate(0, 0, -40).Format("2006-01-02"), DirectBytes: 88888},
	}
	if err := db.Create(&seedSave).Error; err != nil {
		t.Fatalf("seed save_stats: %v", err)
	}
	n1, n2 := uint(1), uint(2)
	sv, ot := saverID, otherID
	seedTraffic := []storage.TrafficLog{
		{UserID: &sv, NodeID: &n1, RxBytes: 600, RecordedAt: now},
		// Someone else's traffic on the same node dilutes the share: saver is half of n1
		{UserID: &ot, NodeID: &n1, RxBytes: 600, RecordedAt: now},
		{UserID: &sv, NodeID: &n2, RxBytes: 100, RecordedAt: now},
		// Last month's traffic stays out of the window
		{UserID: &sv, NodeID: &n1, RxBytes: 5000, RecordedAt: now.AddDate(0, 0, -40)},
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

	// No credentials 404 (the token-counts-as-revoked stance)
	if rec := doPanel(t, r, "", "GET", "/api/panel/savings", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
}

// TestPanelOrdersProviderFilter covers the source filter (operations design
// §record scope): provider=card returns only card-code redemptions,
// provider=epusdt only online purchases, and the default returns the full set.
func TestPanelOrdersProviderFilter(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "grace", map[string]any{
		"grant_type": "extend_days", "grant_value": 30,
	}, 1)
	var u storage.User
	if err := db.First(&u, userID).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doPanel(t, r, u.SubToken, "POST", "/api/panel/redeem", map[string]any{"code": codes[0]}); rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: "epusdt-1-1", UserID: u.ID, Provider: "epusdt", Status: "paid",
	}).Error; err != nil {
		t.Fatal(err)
	}

	count := func(q string) int {
		t.Helper()
		rec := doPanel(t, r, u.SubToken, "GET", "/api/panel/orders"+q, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("orders%s: %d %s", q, rec.Code, rec.Body)
		}
		var list []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return len(list)
	}
	if n := count(""); n != 2 {
		t.Fatalf("全量应 2 条，得 %d", n)
	}
	if n := count("?provider=card"); n != 1 {
		t.Fatalf("provider=card 应 1 条，得 %d", n)
	}
	if n := count("?provider=epusdt"); n != 1 {
		t.Fatalf("provider=epusdt 应 1 条，得 %d", n)
	}
	if n := count("?provider=nosuch"); n != 0 {
		t.Fatalf("未知来源应 0 条，得 %d", n)
	}
}
