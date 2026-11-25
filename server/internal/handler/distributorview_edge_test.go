package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// Batch 21 gap-fill: distributor self-plane edge contracts -- the 401 branches the
// middleware misses (no header / blank bearer / non-numeric ID), the lastAt zero
// value, limit clamping, and the per-endpoint db-down 500 branches (dropped-table
// and query-count injection, both after the middleware's DB re-validation has
// succeeded).

// signDistToken signs a role=distributor JWT (ID controllable, for injecting edge branches).
func signDistToken(t *testing.T, id string) string {
	t.Helper()
	claims := distributorClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        id,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Role: "distributor",
	}
	ss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("ferry-admin-secret"))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return ss
}

// doDistRaw sends a request with the given raw Authorization value (empty string = no header).
func doDistRaw(t *testing.T, r *gin.Engine, auth, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestDistAuthEdgeBranches the 401 branches the middleware misses: no header and
// blank bearer (tokenStr==""), plus a validly signed token with a non-numeric ID
// (ParseUint failure).
func TestDistAuthEdgeBranches(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, auth := range []string{"", "Bearer "} {
		rec := doDistRaw(t, r, auth, http.MethodGet, "/distributor/api/me")
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "令牌缺失") {
			t.Fatalf("auth=%q: %d %s", auth, rec.Code, rec.Body)
		}
	}
	rec := doDistRaw(t, r, "Bearer "+signDistToken(t, "abc"), http.MethodGet, "/distributor/api/me")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "无效令牌") {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}
}

// TestLastAtBranches lastAt's three branches: a non-zero hit returns a pointer, a
// miss returns nil, and a zero time counts as a miss.
func TestLastAtBranches(t *testing.T) {
	now := time.Now()
	if got := lastAt(map[uint]time.Time{7: now}, 7); got == nil || !got.Equal(now) {
		t.Fatalf("hit: got %v", got)
	}
	if got := lastAt(map[uint]time.Time{}, 7); got != nil {
		t.Fatalf("miss: got %v", got)
	}
	if got := lastAt(map[uint]time.Time{7: {}}, 7); got != nil {
		t.Fatalf("zero time: got %v", got)
	}
}

// distEdgeSetup creates distributor d1 plus one of its batches, returning the engine,
// DB handle, distributor token, d1's ID and the batch ID.
func distEdgeSetup(t *testing.T) (*gin.Engine, *gorm.DB, string, uint, uint) {
	t.Helper()
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create d1: %d %s", rec.Code, rec.Body)
	}
	d1 := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))
	rec = doJSON(t, r, "POST", "/api/card-batches", map[string]any{
		"name": "批", "grant_type": "add_quota", "grant_value": 1000,
		"total": 2, "price_cents": 100, "distributor_id": d1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create batch: %d %s", rec.Code, rec.Body)
	}
	batchID := uint(mustJSON(t, rec)["batch"].(map[string]any)["id"].(float64))
	return r, db, distLogin(t, r, "d1", "secret123"), d1, batchID
}

// TestDistViewEmptyScopes empty-data contracts: batches without redemptions
// (redeemed empty) and no batches at all (batchIDs empty) -- both early exits
// return empty lists, not errors.
func TestDistViewEmptyScopes(t *testing.T) {
	r, _, tok, _, _ := distEdgeSetup(t)
	rec := doDist(t, r, tok, http.MethodGet, "/distributor/api/customers")
	if rec.Code != http.StatusOK {
		t.Fatalf("customers: %d %s", rec.Code, rec.Body)
	}
	if v := mustJSON(t, rec)["customers"]; len(v.([]any)) != 0 {
		t.Fatalf("customers not empty: %s", rec.Body)
	}

	// Second distributor: no batches.
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d2", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create d2: %d %s", rec.Code, rec.Body)
	}
	d2Tok := distLogin(t, r, "d2", "secret123")
	for _, path := range []string{"/distributor/api/customers", "/distributor/api/batches"} {
		rec = doDist(t, r, d2Tok, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("d2 %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	rec = doDist(t, r, d2Tok, http.MethodGet, "/distributor/api/ledger")
	if rec.Code != http.StatusOK {
		t.Fatalf("d2 ledger: %d %s", rec.Code, rec.Body)
	}
	if v := mustJSON(t, rec)["ledger"]; len(v.([]any)) != 0 {
		t.Fatalf("d2 ledger not empty: %s", rec.Body)
	}
}

// TestDistViewLimitClamps limit clamping for orders/ledger: parses normally, <1
// clamps to 1, >200 clamps to 200 (ledger rows and distributor orders inserted
// directly).
func TestDistViewLimitClamps(t *testing.T) {
	r, db, tok, d1, _ := distEdgeSetup(t)
	for i, kind := range []string{"sale", "commission", "adjust"} {
		if err := db.Create(&storage.DistributorLedger{
			DistributorID: d1, OrderNo: fmt.Sprintf("L%d", i), Kind: kind, AmountCents: 100,
		}).Error; err != nil {
			t.Fatalf("ledger seed: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := db.Create(&storage.PaymentOrder{
			OrderNo: fmt.Sprintf("T%d", i), UserID: 1, Provider: "card",
			AmountCents: 100, DistributorID: d1, Status: "paid",
		}).Error; err != nil {
			t.Fatalf("order seed: %v", err)
		}
	}
	orders := func(query string) int {
		t.Helper()
		rec := doDist(t, r, tok, http.MethodGet, "/distributor/api/orders"+query)
		if rec.Code != http.StatusOK {
			t.Fatalf("orders%s: %d %s", query, rec.Code, rec.Body)
		}
		return len(mustJSON(t, rec)["orders"].([]any))
	}
	if n := orders("?limit=5"); n != 2 {
		t.Fatalf("limit=5: got %d", n)
	}
	if n := orders("?limit=0"); n != 1 {
		t.Fatalf("limit=0 clamp: got %d", n)
	}
	if n := orders("?limit=999"); n != 2 {
		t.Fatalf("limit=999 clamp: got %d", n)
	}

	ledger := func(query string) (int, float64) {
		t.Helper()
		rec := doDist(t, r, tok, http.MethodGet, "/distributor/api/ledger"+query)
		if rec.Code != http.StatusOK {
			t.Fatalf("ledger%s: %d %s", query, rec.Code, rec.Body)
		}
		out := mustJSON(t, rec)
		return len(out["ledger"].([]any)), out["balance_cents"].(float64)
	}
	if n, _ := ledger("?limit=2"); n != 2 {
		t.Fatalf("ledger limit=2: got %d", n)
	}
	if n, _ := ledger("?limit=0"); n != 1 {
		t.Fatalf("ledger limit=0 clamp: got %d", n)
	}
	if n, bal := ledger("?limit=999"); n != 3 || bal != 200 {
		t.Fatalf("ledger limit=999 clamp: n=%d bal=%v", n, bal)
	}
}

// TestDistViewDBFailures the per-endpoint db-down 500 branches: dropped tables
// target the first/second query, query-count injection targets middle queries
// (the middleware's DB re-validation has already passed).
func TestDistViewDBFailures(t *testing.T) {
	want500 := func(t *testing.T, rec *httptest.ResponseRecorder, what string) {
		t.Helper()
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: %d %s", what, rec.Code, rec.Body)
		}
	}

	t.Run("me totals fail", func(t *testing.T) {
		r, db, tok, _, _ := distEdgeSetup(t)
		db.Migrator().DropTable(&storage.DistributorLedger{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/me"), "me totals")
	})
	t.Run("me reread fail", func(t *testing.T) {
		r, db, tok, _, _ := distEdgeSetup(t)
		// middleware First succeeds, endpoint First fails: inject on the second *storage.Distributor query.
		var reads int
		db.Callback().Query().Before("gorm:query").Register("test:dist_reads", func(tx *gorm.DB) {
			if _, ok := tx.Statement.Dest.(*storage.Distributor); ok {
				reads++
				if reads >= 2 {
					tx.AddError(errors.New("db down"))
				}
			}
		})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/me"), "me reread")
	})
	t.Run("batches list fail", func(t *testing.T) {
		r, db, tok, _, _ := distEdgeSetup(t)
		db.Migrator().DropTable(&storage.CardBatch{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/batches"), "batches list")
	})
	t.Run("batches count fail", func(t *testing.T) {
		r, db, tok, _, _ := distEdgeSetup(t)
		db.Migrator().DropTable(&storage.CardCode{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/batches"), "batches count")
	})
	t.Run("codes bad param", func(t *testing.T) {
		r, _, tok, _, _ := distEdgeSetup(t)
		rec := doDist(t, r, tok, http.MethodGet, "/distributor/api/batches/abc/codes")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad param: %d %s", rec.Code, rec.Body)
		}
	})
	t.Run("codes batch miss fail", func(t *testing.T) {
		r, db, tok, _, batchID := distEdgeSetup(t)
		db.Migrator().DropTable(&storage.CardBatch{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/batches/"+utoa(batchID)+"/codes"), "codes batch miss")
	})
	t.Run("codes list fail", func(t *testing.T) {
		r, db, tok, _, batchID := distEdgeSetup(t)
		db.Migrator().DropTable(&storage.CardCode{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/batches/"+utoa(batchID)+"/codes"), "codes list")
	})
	t.Run("customers pluck fail", func(t *testing.T) {
		r, db, tok, _, _ := distEdgeSetup(t)
		db.Migrator().DropTable(&storage.CardBatch{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/customers"), "customers pluck")
	})
	t.Run("customers redeemed fail", func(t *testing.T) {
		r, db, tok, _, _ := distEdgeSetup(t)
		db.Migrator().DropTable(&storage.CardCode{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/customers"), "customers redeemed")
	})
	t.Run("customers users fail", func(t *testing.T) {
		r, db, tok, _, batchID := distEdgeSetup(t)
		distEdgeRedeem(t, r, db, batchID)
		db.Migrator().DropTable(&storage.User{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/customers"), "customers users")
	})
	t.Run("customers usage fail", func(t *testing.T) {
		r, db, tok, _, batchID := distEdgeSetup(t)
		distEdgeRedeem(t, r, db, batchID)
		db.Migrator().DropTable(&storage.TrafficLog{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/customers"), "customers usage")
	})
	t.Run("orders list fail", func(t *testing.T) {
		r, db, tok, _, _ := distEdgeSetup(t)
		db.Migrator().DropTable(&storage.PaymentOrder{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/orders"), "orders list")
	})
	t.Run("orders users fail", func(t *testing.T) {
		r, db, tok, d1, _ := distEdgeSetup(t)
		if err := db.Create(&storage.PaymentOrder{
			OrderNo: "T-EDGE", UserID: 1, Provider: "card",
			AmountCents: 100, DistributorID: d1, Status: "paid",
		}).Error; err != nil {
			t.Fatalf("order seed: %v", err)
		}
		db.Migrator().DropTable(&storage.User{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/orders"), "orders users")
	})
	t.Run("ledger rows fail", func(t *testing.T) {
		r, db, tok, _, _ := distEdgeSetup(t)
		db.Migrator().DropTable(&storage.DistributorLedger{})
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/ledger"), "ledger rows")
	})
	t.Run("ledger totals fail", func(t *testing.T) {
		r, db, tok, _, _ := distEdgeSetup(t)
		// rows go through Find (gorm:query chain), totals through Scan (gorm:row
		// chain) -- hook one counter onto both chains: inject failure on the second
		// distributor_ledgers statement.
		var reads int
		count := func(tx *gorm.DB) {
			_, byModel := tx.Statement.Model.(*storage.DistributorLedger)
			_, byDest := tx.Statement.Dest.(*[]storage.DistributorLedger)
			if byModel || byDest {
				reads++
				if reads >= 2 {
					tx.AddError(errors.New("db down"))
				}
			}
		}
		db.Callback().Query().Before("gorm:query").Register("test:ledger_q", count)
		db.Callback().Row().Before("gorm:row").Register("test:ledger_r", count)
		want500(t, doDist(t, r, tok, http.MethodGet, "/distributor/api/ledger"), "ledger totals")
	})
}

// distEdgeRedeem creates a user and redeems one card from the batch (the customer-side aggregation entry).
func distEdgeRedeem(t *testing.T, r *gin.Engine, db *gorm.DB, batchID uint) {
	t.Helper()
	rec := doJSON(t, r, http.MethodPost, "/api/users", map[string]any{"username": "alice", "quota_bytes": 1000})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	uid := uint(mustJSON(t, rec)["id"].(float64))
	var code storage.CardCode
	if err := db.Where("batch_id = ?", batchID).First(&code).Error; err != nil {
		t.Fatalf("pick code: %v", err)
	}
	rec = doJSON(t, r, http.MethodPost, "/api/redeem", map[string]any{"code": code.Code, "user_id": uid})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
}
