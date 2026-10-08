package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// distLogin 走登录端点取代理令牌。
func distLogin(t *testing.T, r *gin.Engine, username, password string) string {
	t.Helper()
	rec := doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": username, "password": password})
	if rec.Code != http.StatusOK {
		t.Fatalf("dist login: %d %s", rec.Code, rec.Body)
	}
	return mustJSON(t, rec)["token"].(string)
}

// doDist 带代理令牌发请求（自面端点均为 GET 只读）。
func doDist(t *testing.T, r *gin.Engine, token, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDistributorSelfView(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, _ := redeemCreate(t, r, "alice", map[string]any{"grant_type": "add_quota", "grant_value": 1000}, 1)
	_, selfCodes := redeemCreate(t, r, "bob", map[string]any{"grant_type": "add_quota", "grant_value": 1000, "price_cents": 800}, 1)

	// 两代理 + 各自批次（d1 佣金 30、d2 佣金 10）。
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "secret123", "discount_percent": 30})
	d1 := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d2", "password": "secret123", "discount_percent": 10})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create d2: %d %s", rec.Code, rec.Body)
	}
	mkBatch := func(name string, dist uint, total int) []string {
		body := map[string]any{"name": name, "grant_type": "add_quota", "grant_value": 1000, "total": total, "price_cents": 1000}
		if dist > 0 {
			body["distributor_id"] = dist
		}
		rec := doJSON(t, r, "POST", "/api/card-batches", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("batch %s: %d %s", name, rec.Code, rec.Body)
		}
		out := []string{}
		for _, c := range mustJSON(t, rec)["codes"].([]any) {
			out = append(out, c.(string))
		}
		return out
	}
	b1 := mkBatch("d1 批次", d1, 3)
	b2 := mkBatch("d2 批次", 0, 1) // 先建一个自营批次占位
	_ = b2

	// 无令牌/乱令牌 401。
	rec = doJSON(t, r, "GET", "/distributor/api/me", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}
	req := httptest.NewRequest("GET", "/distributor/api/me", nil)
	req.Header.Set("Authorization", "Bearer not-a-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d %s", w.Code, w.Body)
	}

	token := distLogin(t, r, "d1", "secret123")

	// me：资料+四元组。
	rec = doDist(t, r, token, "GET", "/distributor/api/me")
	if rec.Code != http.StatusOK {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	me := mustJSON(t, rec)
	if me["username"] != "d1" || me["discount_percent"].(float64) != 30 {
		t.Fatalf("me wrong: %s", rec.Body)
	}

	// batches：只回自有批次。
	rec = doDist(t, r, token, "GET", "/distributor/api/batches")
	var batches []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &batches)
	if len(batches) != 1 || batches[0]["name"] != "d1 批次" || batches[0]["remaining"].(float64) != 3 {
		t.Fatalf("batches: %s", rec.Body)
	}
	b1ID := uint(batches[0]["id"].(float64))

	// codes：自有批次回卡密；他人/自营批次 404（互不可见）。
	rec = doDist(t, r, token, "GET", "/distributor/api/batches/"+utoa(b1ID)+"/codes")
	var codes []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &codes)
	if len(codes) != 3 {
		t.Fatalf("codes: %s", rec.Body)
	}
	rec = doDist(t, r, token, "GET", "/distributor/api/batches/999/codes")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("ghost batch: %d %s", rec.Code, rec.Body)
	}

	// 兑换一张：d1 批次归 alice、自营批次归 bob。
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": b1[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem d1: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": selfCodes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem self: %d %s", rec.Code, rec.Body)
	}

	// customers：只含兑换过自有卡的用户（alice， redeemed_cnt=1）。
	rec = doDist(t, r, token, "GET", "/distributor/api/customers")
	if rec.Code != http.StatusOK {
		t.Fatalf("customers: %d %s", rec.Code, rec.Body)
	}
	var cust struct {
		Customers []map[string]any `json:"customers"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cust)
	if len(cust.Customers) != 1 {
		t.Fatalf("customers: %s", rec.Body)
	}
	if cust.Customers[0]["username"] != "alice" || cust.Customers[0]["redeemed_cnt"].(float64) != 1 {
		t.Fatalf("customer row: %s", rec.Body)
	}

	// orders：只有归属自己的 card 单，带用户名。
	rec = doDist(t, r, token, "GET", "/distributor/api/orders")
	var ord struct {
		Orders []map[string]any `json:"orders"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ord)
	if len(ord.Orders) != 1 || ord.Orders[0]["username"] != "alice" || ord.Orders[0]["distributor_id"].(float64) != float64(d1) {
		t.Fatalf("orders: %s", rec.Body)
	}

	// ledger：sale+commission 两行、余额=300。
	rec = doDist(t, r, token, "GET", "/distributor/api/ledger")
	var led struct {
		Ledger       []map[string]any `json:"ledger"`
		BalanceCents float64          `json:"balance_cents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &led)
	if len(led.Ledger) != 2 || led.BalanceCents != 300 {
		t.Fatalf("ledger: %s", rec.Body)
	}

	// me 四元组更新：sale=1000/commission=300。
	rec = doDist(t, r, token, "GET", "/distributor/api/me")
	me = mustJSON(t, rec)
	if me["sale_cents"].(float64) != 1000 || me["commission_cents"].(float64) != 300 || me["balance_cents"].(float64) != 300 {
		t.Fatalf("me totals: %s", rec.Body)
	}

	// 停用即踢：token 立即失效（中间件回库校验）。
	if err := db.Model(&storage.Distributor{}).Where("id = ?", d1).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}
	rec = doDist(t, r, token, "GET", "/distributor/api/me")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled token: %d %s", rec.Code, rec.Body)
	}
}
