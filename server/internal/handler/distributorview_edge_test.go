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

// 二十一批补口：代理自面边沿口径——中间件未走到的 401 分支（无头/Bearer 空白/
// ID 非数字）、lastAt 零值、limit 夹取、以及各端点库故障 500 分支（拆表与
// 查询计数注入，均在中间件回库校验成功之后注入）。

// signDistToken 签 role=distributor 的 JWT（ID 可控，供边沿分支注入）。
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

// doDistRaw 带指定 Authorization 原文发请求（空串=不带头）。
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

// TestDistAuthEdgeBranches 中间件未走到的 401 分支：无头与 Bearer 空白
// （tokenStr==""）、签名合法但 ID 非数字（ParseUint 失败）。
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

// TestLastAtBranches lastAt 三分支：命中非零时间回指针、未命中回 nil、零值
// 时间视同未命中。
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

// distEdgeSetup 建代理 d1 + 名下批次，返回引擎、库句柄、代理令牌、d1 与批次 id。
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

// TestDistViewEmptyScopes 空数据口径：有批次无兑换（redeemed 空）、无批次
// （batchIDs 空）——两种早退都回空列表，不报错。
func TestDistViewEmptyScopes(t *testing.T) {
	r, _, tok, _, _ := distEdgeSetup(t)
	rec := doDist(t, r, tok, http.MethodGet, "/distributor/api/customers")
	if rec.Code != http.StatusOK {
		t.Fatalf("customers: %d %s", rec.Code, rec.Body)
	}
	if v := mustJSON(t, rec)["customers"]; len(v.([]any)) != 0 {
		t.Fatalf("customers not empty: %s", rec.Body)
	}

	// 第二个代理：无批次。
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

// TestDistViewLimitClamps orders/ledger 的 limit 夹取：正常解析、<1 夹 1、
// >200 夹 200（直插账目行与代理订单）。
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

// TestDistViewDBFailures 各端点库故障 500 分支：拆表定向破坏首查/次查，
// 查询计数注入破坏中间查询（中间件回库校验已通过）。
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
		// 中间件 First 成功、端点 First 失败：第二次 *storage.Distributor 查询注入。
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
		// rows 走 Find（gorm:query 链）、totals 走 Scan（gorm:row 链）——同一计数
		// 器挂两条链：第二次 distributor_ledgers 语句注入失败。
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

// distEdgeRedeem 造用户并兑换批次里的一张卡（客户侧聚合入口）。
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
