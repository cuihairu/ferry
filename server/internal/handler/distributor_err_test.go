package handler

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Batch 25 gap-filling: the 400/404/500 conventions across the distributor
// center — login rate-limit locking and DB fault, create with bad binding and
// an over-long password (rejected by bcrypt), disable write-back and persist
// failure, update with each validation and the re-find fallback failing,
// ledger limit clamping with the two-stage queries failing separately, and
// ledger ops with binding/aggregate/posting failures.
// Gaps left: the HS256 signing branch of JWT issuance — a string key can
// always sign, so the error path is unreachable and is not forced.

// isDistModel reports whether a statement lands on the distributor table (on the Find chain Model is a slice pointer).
func isDistModel(v any) bool {
	switch v.(type) {
	case *storage.Distributor, []storage.Distributor, *[]storage.Distributor:
		return true
	}
	return false
}

// isDistLedgerModel reports whether a statement lands on the distributor ledger table.
func isDistLedgerModel(v any) bool {
	switch v.(type) {
	case *storage.DistributorLedger, []storage.DistributorLedger, *[]storage.DistributorLedger:
		return true
	}
	return false
}

// failDistQueriesAt fails the n-th query (1-based) against the distributor
// table: the post-update re-find fallback is targeted by that order.
func failDistQueriesAt(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:dist_query_at", func(tx *gorm.DB) {
		if !isDistModel(tx.Statement.Model) {
			return
		}
		seen++
		if seen == n {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failDistCreates makes distributor row creation fail (a DB fault other than uniqueness).
func failDistCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:dist_create", func(tx *gorm.DB) {
		if isDistModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failDistUpdates makes distributor row updates fail (disable write-back / partial update).
func failDistUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:dist_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Distributor); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failDistLedgerQueries makes the ledger table's Find fail (reconciliation list fetch).
func failDistLedgerQueries(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Query().Before("gorm:query").Register("test:distledger_query", func(tx *gorm.DB) {
		if isDistLedgerModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failDistLedgerAt fails the n-th statement (1-based) against the ledger
// table: within one sample, Find goes through the gorm:query chain while the
// aggregate Scan goes through the gorm:row chain, so both chains must share
// one counter to target a statement by order.
func failDistLedgerAt(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	seen := 0
	hit := func(tx *gorm.DB) {
		if !isDistLedgerModel(tx.Statement.Model) {
			return
		}
		seen++
		if seen == n {
			tx.AddError(errors.New("db down"))
		}
	}
	db.Callback().Query().Before("gorm:query").Register("test:distledger_q_at", hit)
	db.Callback().Row().Before("gorm:row").Register("test:distledger_r_at", hit)
}

// failDistLedgerCreates makes ledger row posting fail.
func failDistLedgerCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:distledger_create", func(tx *gorm.DB) {
		if isDistLedgerModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// seedDist creates a distributor account via the admin side and returns the row id.
func seedDist(t *testing.T, r *gin.Engine, db *gorm.DB, name string) uint {
	t.Helper()
	rec := doJSON(t, r, http.MethodPost, "/api/distributors",
		map[string]any{"username": name, "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create dist %s: %d %s", name, rec.Code, rec.Body)
	}
	var d storage.Distributor
	if err := db.Where("username = ?", name).First(&d).Error; err != nil {
		t.Fatalf("find dist %s: %v", name, err)
	}
	return d.ID
}

// TestDistributorLoginErrors: login — rate-limit locking, bad binding, and DB fault.
func TestDistributorLoginErrors(t *testing.T) {
	// Locking kicks in once consecutive failures reach the threshold: the lock lands after the fifth failure and the sixth request gets 429 outright.
	r, db := newTestRouterWithDB(t)
	seedDist(t, r, db, "login-l")
	for i := 0; i < 5; i++ {
		if rec := doJSON(t, r, http.MethodPost, "/distributor/login",
			map[string]any{"username": "login-l", "password": "wrong"}); rec.Code != http.StatusUnauthorized {
			t.Fatalf("fail %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	if rec := doJSON(t, r, http.MethodPost, "/distributor/login",
		map[string]any{"username": "login-l", "password": "secret123"}); rec.Code != http.StatusTooManyRequests ||
		!strings.Contains(rec.Body.String(), "尝试过于频繁") {
		t.Fatalf("locked: %d %s", rec.Code, rec.Body)
	}

	// Bad binding: 400.
	r, _ = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/distributor/login", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// DB fault: 500 (internal error, never leaked).
	r, db = newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/distributor/login",
		map[string]any{"username": "x", "password": "y"}); rec.Code != http.StatusInternalServerError ||
		!strings.Contains(rec.Body.String(), "内部错误") {
		t.Fatalf("db: %d %s", rec.Code, rec.Body)
	}
}

// TestListDistributorsErrors: list — distributor fetch failure and aggregate failure each surface as 500.
func TestListDistributorsErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "list-l")
	failDistLedgerAt(t, db, 1) // the ledger table's first statement is the aggregate (the Query chain has no preceding statement)
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("totals: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateDistributorErrors: create — bad binding, an over-long password
// rejected by bcrypt, disable write-back failure, and persist failure.
func TestCreateDistributorErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// bcrypt rejects anything over 72 bytes, surfacing as 500.
	r, _ = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors", map[string]any{
		"username": "longpw", "password": strings.Repeat("p", 100),
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("long password: %d %s", rec.Code, rec.Body)
	}

	// Note persisted + explicit disable written back (Enabled has default:true, so the zero value would be skipped).
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, http.MethodPost, "/api/distributors", map[string]any{
		"username": "note-l", "password": "secret123",
		"note": "  华东渠道 ", "enabled": false,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create disabled: %d %s", rec.Code, rec.Body)
	}
	var d storage.Distributor
	if err := db.Where("username = ?", "note-l").First(&d).Error; err != nil {
		t.Fatalf("find: %v", err)
	}
	if d.Enabled || d.Note != "华东渠道" {
		t.Fatalf("not persisted: %+v", d)
	}

	// Disable write-back failure: 500.
	r, db = newTestRouterWithDB(t)
	failDistUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors",
		map[string]any{"username": "wb-fail", "password": "secret123", "enabled": false}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("write-back: %d %s", rec.Code, rec.Body)
	}

	// Persist failure (not a uniqueness conflict) surfaces as 500.
	r, db = newTestRouterWithDB(t)
	failDistCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors",
		map[string]any{"username": "create-fail", "password": "secret123"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateDistributorErrors: update — locate validation, password reset and
// validation failure, each write-DB fault, and the re-find fallback failing.
func TestUpdateDistributorErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/abc", map[string]any{"enabled": false}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-l")
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-db")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1", map[string]any{"enabled": false}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}

	// Validation surface: discount out of range and no updatable fields.
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-val")
	for _, tc := range []struct {
		name string
		body any
	}{
		{"discount", map[string]any{"discount_percent": 150}},
		{"empty", map[string]any{}},
	} {
		if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1", tc.body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", tc.name, rec.Code, rec.Body)
		}
	}

	// Password reset succeeds (the hash is rewritten, the plaintext never leaks).
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-pw")
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1",
		map[string]any{"password": "newsecret1"}); rec.Code != http.StatusOK {
		t.Fatalf("password set: %d %s", rec.Code, rec.Body)
	}
	var d storage.Distributor
	if err := db.First(&d, 1).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if d.PasswordHash == "" || d.PasswordHash == "newsecret1" {
		t.Fatalf("hash not rewritten: %q", d.PasswordHash)
	}

	// Over-long password: 500.
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-pwbad")
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1",
		map[string]any{"password": strings.Repeat("p", 100)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("long password: %d %s", rec.Code, rec.Body)
	}

	// Update statement failure: 500.
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-write")
	failDistUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1",
		map[string]any{"discount_percent": 10}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("updates: %d %s", rec.Code, rec.Body)
	}

	// Re-find fallback failure: 500 (locate 1 -> update -> re-check).
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-refind")
	failDistQueriesAt(t, db, 2)
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1",
		map[string]any{"discount_percent": 10}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("re-find: %d %s", rec.Code, rec.Body)
	}
}

// TestDistributorLedgerErrors: ledger — locate validation, limit clamping, and the fetch and aggregate failing separately.
func TestDistributorLedgerErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors/abc/ledger", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors/1/ledger", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}

	// limit conventions: a parse failure falls back to the default 50, a legal value takes effect, and out-of-range values clamp to 1 and 200.
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "led-l")
	for _, limit := range []string{"abc", "10", "0", "500"} {
		if rec := doJSON(t, r, http.MethodGet, "/api/distributors/1/ledger?limit="+limit, nil); rec.Code != http.StatusOK {
			t.Fatalf("limit=%s: %d %s", limit, rec.Code, rec.Body)
		}
	}

	// Fetch failure: 500.
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "led-find")
	failDistLedgerQueries(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors/1/ledger", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("rows: %d %s", rec.Code, rec.Body)
	}

	// Aggregate failure: 500 (fetch 1 goes through the Query chain, aggregate 2 through the Row chain).
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "led-totals")
	failDistLedgerAt(t, db, 2)
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors/1/ledger", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("totals: %d %s", rec.Code, rec.Body)
	}
}

// TestWriteLedgerOpErrors: ledger ops — locate validation, bad binding, aggregate failure, and posting failure.
func TestWriteLedgerOpErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, kind := range []string{"payout", "adjust"} {
		if rec := doJSON(t, r, http.MethodPost, "/api/distributors/abc/"+kind,
			map[string]any{"amount_cents": 100}); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s bad id: %d %s", kind, rec.Code, rec.Body)
		}
	}

	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors/1/payout",
		map[string]any{"amount_cents": 100}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "op-bind")
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors/1/adjust", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// Payout rejects a non-positive amount (manual compensation goes through adjust; zero payouts are not sent).
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors/1/payout",
		map[string]any{"amount_cents": 0}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "amount_cents must be") {
		t.Fatalf("payout zero: %d %s", rec.Code, rec.Body)
	}

	// Aggregate failure: 500 (validation passed; no ledger statement precedes the fetch).
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "op-totals")
	failDistLedgerAt(t, db, 1)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors/1/adjust",
		map[string]any{"amount_cents": 100}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("totals: %d %s", rec.Code, rec.Body)
	}

	// Posting failure: 500 (adjust is not balance-bound — validation passes first, then the Create runs).
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "op-create")
	failDistLedgerCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors/1/adjust",
		map[string]any{"amount_cents": 100}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
}
