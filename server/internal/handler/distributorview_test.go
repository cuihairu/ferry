package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// distLogin hits the login endpoint to obtain a distributor token.
func distLogin(t *testing.T, r *gin.Engine, username, password string) string {
	t.Helper()
	rec := doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": username, "password": password})
	if rec.Code != http.StatusOK {
		t.Fatalf("dist login: %d %s", rec.Code, rec.Body)
	}
	return mustJSON(t, rec)["token"].(string)
}

// doDist sends a request carrying the distributor token (all self-plane endpoints are read-only GETs).
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

	// Two distributors plus their own batches (d1 discount 30, d2 discount 10).
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
	b2 := mkBatch("d2 批次", 0, 1) // create a self-operated batch first as a placeholder
	_ = b2

	// No token / garbage token -> 401.
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

	// me: profile plus the four counters.
	rec = doDist(t, r, token, "GET", "/distributor/api/me")
	if rec.Code != http.StatusOK {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	me := mustJSON(t, rec)
	if me["username"] != "d1" || me["discount_percent"].(float64) != 30 {
		t.Fatalf("me wrong: %s", rec.Body)
	}

	// batches: only own batches are returned.
	rec = doDist(t, r, token, "GET", "/distributor/api/batches")
	var batches []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &batches)
	if len(batches) != 1 || batches[0]["name"] != "d1 批次" || batches[0]["remaining"].(float64) != 3 {
		t.Fatalf("batches: %s", rec.Body)
	}
	b1ID := uint(batches[0]["id"].(float64))

	// codes: own batches return card codes; others' and self-operated batches 404 (mutually invisible).
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

	// Redeem one card: the d1 batch goes to alice, the self-operated batch to bob.
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": b1[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem d1: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": selfCodes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem self: %d %s", rec.Code, rec.Body)
	}

	// customers: only users who redeemed own cards (alice, redeemed_cnt=1).
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

	// orders: only card orders attributed to this distributor, carrying the username.
	rec = doDist(t, r, token, "GET", "/distributor/api/orders")
	var ord struct {
		Orders []map[string]any `json:"orders"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ord)
	if len(ord.Orders) != 1 || ord.Orders[0]["username"] != "alice" || ord.Orders[0]["distributor_id"].(float64) != float64(d1) {
		t.Fatalf("orders: %s", rec.Body)
	}

	// ledger: two rows, sale + commission, balance=300.
	rec = doDist(t, r, token, "GET", "/distributor/api/ledger")
	var led struct {
		Ledger       []map[string]any `json:"ledger"`
		BalanceCents float64          `json:"balance_cents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &led)
	if len(led.Ledger) != 2 || led.BalanceCents != 300 {
		t.Fatalf("ledger: %s", rec.Body)
	}

	// me four-counter update: sale=1000/commission=300.
	rec = doDist(t, r, token, "GET", "/distributor/api/me")
	me = mustJSON(t, rec)
	if me["sale_cents"].(float64) != 1000 || me["commission_cents"].(float64) != 300 || me["balance_cents"].(float64) != 300 {
		t.Fatalf("me totals: %s", rec.Body)
	}

	// Disabling kicks out: the token becomes invalid immediately (middleware re-validates against the DB).
	if err := db.Model(&storage.Distributor{}).Where("id = ?", d1).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}
	rec = doDist(t, r, token, "GET", "/distributor/api/me")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled token: %d %s", rec.Code, rec.Body)
	}
}
