package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// utoa is the test-local shorthand for turning an id into a string.
func utoa(id uint) string { return strconv.FormatUint(uint64(id), 10) }

// mustJSON decodes the response into a map and Fails on error.
func mustJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return m
}

func TestDistributorCRUD(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Validation surface: missing username/missing password/commission out of range.
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"password": "x12345"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing username: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing password: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "x12345", "discount_percent": 101})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("discount>100: %d %s", rec.Code, rec.Body)
	}

	// Create succeeds: default commission 0, enabled, the hash never leaks.
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		Distributor map[string]any `json:"distributor"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Distributor["password_hash"] != nil {
		t.Fatalf("password_hash leaked: %s", rec.Body)
	}
	if created.Distributor["discount_percent"].(float64) != 0 || created.Distributor["enabled"] != true {
		t.Fatalf("defaults wrong: %s", rec.Body)
	}
	id := uint(created.Distributor["id"].(float64))

	// Duplicate username: 409.
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "x12345"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("dup username: %d %s", rec.Code, rec.Body)
	}

	// Partial update: commission 30 + note.
	rec = doJSON(t, r, "PUT", "/api/distributors/"+utoa(id), map[string]any{"discount_percent": 30, "note": "华东渠道"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var d storage.Distributor
	if err := db.First(&d, id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if d.DiscountPercent != 30 || d.Note != "华东渠道" {
		t.Fatalf("update lost: %+v", d)
	}

	// List carries the balance (initially 0).
	rec = doJSON(t, r, "GET", "/api/distributors", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Distributors []struct {
			Username     string `json:"username"`
			BalanceCents int64  `json:"balance_cents"`
		} `json:"distributors"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Distributors) != 1 || list.Distributors[0].BalanceCents != 0 {
		t.Fatalf("list: %s", rec.Body)
	}

	// Ledger empty state: balance 0, no rows.
	rec = doJSON(t, r, "GET", "/api/distributors/"+utoa(id)+"/ledger", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ledger: %d %s", rec.Code, rec.Body)
	}

	// Non-existent distributor: update and ledger both 404.
	rec = doJSON(t, r, "PUT", "/api/distributors/999", map[string]any{"enabled": false})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("update 404: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/distributors/999/ledger", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("ledger 404: %d %s", rec.Code, rec.Body)
	}
}

func TestDistributorLogin(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))

	// Success: the token is non-empty and the commission echoes back.
	rec = doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": "d1", "password": "secret123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	var login struct {
		Token           string `json:"token"`
		DiscountPercent int    `json:"discount_percent"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &login)
	if login.Token == "" {
		t.Fatalf("no token: %s", rec.Body)
	}

	// Wrong password/unknown user: 401.
	rec = doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": "d1", "password": "wrong"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": "ghost", "password": "x"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user: %d %s", rec.Code, rec.Body)
	}

	// Disabling blocks login.
	rec = doJSON(t, r, "PUT", "/api/distributors/"+utoa(id), map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": "d1", "password": "secret123"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled login: %d %s", rec.Code, rec.Body)
	}
}

func TestRedeemDistributorLedger(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, _ := redeemCreate(t, r, "alice", map[string]any{"grant_type": "add_quota", "grant_value": 1000}, 1)

	// Distributor account: commission 30%.
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "secret123", "discount_percent": 30})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create dist: %d %s", rec.Code, rec.Body)
	}
	distID := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))

	// Distributor batch: list price 1000 cents.
	rec = doJSON(t, r, "POST", "/api/card-batches", map[string]any{
		"name": "代理批次", "grant_type": "add_quota", "grant_value": 5000,
		"total": 3, "price_cents": 1000, "distributor_id": distID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("dist batch: %d %s", rec.Code, rec.Body)
	}
	distCodes := mustJSON(t, rec)["codes"].([]any)

	// Redeem a distributor card: the order carries the attribution, sale(+1000) + commission(+300) share one order_no.
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": distCodes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
	orderNo := mustJSON(t, rec)["order_no"].(string)
	var order storage.PaymentOrder
	if err := db.Where("order_no = ?", orderNo).First(&order).Error; err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.DistributorID != distID {
		t.Fatalf("order distributor: %d", order.DistributorID)
	}
	var ledger []storage.DistributorLedger
	if err := db.Where("distributor_id = ?", distID).Order("kind").Find(&ledger).Error; err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if len(ledger) != 2 {
		t.Fatalf("want 2 ledger rows, got %d: %+v", len(ledger), ledger)
	}
	sale, commission := ledger[0], ledger[1]
	if sale.Kind == "commission" {
		sale, commission = commission, sale
	}
	if sale.Kind != "sale" || sale.AmountCents != 1000 || sale.OrderNo != orderNo {
		t.Fatalf("sale row wrong: %+v", sale)
	}
	if commission.Kind != "commission" || commission.AmountCents != 300 || commission.OrderNo != orderNo {
		t.Fatalf("commission row wrong: %+v", commission)
	}
	// Balance = commission 300 (sale is a trace entry and never enters the balance).
	rec = doJSON(t, r, "GET", "/api/distributors/"+utoa(distID)+"/ledger", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ledger api: %d %s", rec.Code, rec.Body)
	}
	if got := mustJSON(t, rec)["balance_cents"].(float64); got != 300 {
		t.Fatalf("balance: %v %s", got, rec.Body)
	}

	// Self-operated batches never post ledger rows.
	_, selfCodes := redeemCreate(t, r, "bob", map[string]any{"grant_type": "add_quota", "grant_value": 1000, "price_cents": 800}, 1)
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": selfCodes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("self redeem: %d %s", rec.Code, rec.Body)
	}
	var cnt int64
	db.Model(&storage.DistributorLedger{}).Count(&cnt)
	if cnt != 2 {
		t.Fatalf("self-owned batch wrote ledger: %d", cnt)
	}

	// Commission 0: only one sale row is posted.
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d2", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create d2: %d %s", rec.Code, rec.Body)
	}
	d2 := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))
	rec = doJSON(t, r, "POST", "/api/card-batches", map[string]any{
		"name": "零佣金批次", "grant_type": "add_quota", "grant_value": 1000,
		"total": 1, "price_cents": 500, "distributor_id": d2,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("d2 batch: %d %s", rec.Code, rec.Body)
	}
	d2codes := mustJSON(t, rec)["codes"].([]any)
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": d2codes[0].(string), "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("d2 redeem: %d %s", rec.Code, rec.Body)
	}
	var d2rows []storage.DistributorLedger
	db.Where("distributor_id = ?", d2).Find(&d2rows)
	if len(d2rows) != 1 || d2rows[0].Kind != "sale" || d2rows[0].AmountCents != 500 {
		t.Fatalf("d2 ledger: %+v", d2rows)
	}

	// Disabled distributor: cards still redeem and revenue share still posts (entitlements must be honored; the accounts stay).
	if err := db.Model(&storage.Distributor{}).Where("id = ?", distID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": distCodes[1].(string), "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled-dist redeem: %d %s", rec.Code, rec.Body)
	}
	var rows2 int64
	db.Model(&storage.DistributorLedger{}).Where("distributor_id = ?", distID).Count(&rows2)
	if rows2 != 4 {
		t.Fatalf("disabled dist ledger rows: %d", rows2)
	}

	// Attribution pointing at a non-existent distributor: create batch 400.
	rec = doJSON(t, r, "POST", "/api/card-batches", map[string]any{
		"name": "幽灵批次", "grant_type": "add_quota", "grant_value": 1000,
		"total": 1, "distributor_id": 999,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ghost distributor batch: %d %s", rec.Code, rec.Body)
	}

	// Reconciliation quadruple (DS-2 distributor dimension): d1's two distributor cards -> sale=2000, commission=600.
	rec = doJSON(t, r, "GET", "/api/distributors", nil)
	var list struct {
		Distributors []struct {
			Username        string `json:"username"`
			SaleCents       int64  `json:"sale_cents"`
			CommissionCents int64  `json:"commission_cents"`
			PayoutCents     int64  `json:"payout_cents"`
			BalanceCents    int64  `json:"balance_cents"`
		} `json:"distributors"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	for _, d := range list.Distributors {
		if d.Username == "d1" {
			if d.SaleCents != 2000 || d.CommissionCents != 600 || d.PayoutCents != 0 || d.BalanceCents != 600 {
				t.Fatalf("d1 totals: %+v", d)
			}
		}
	}
}

func TestDistributorPayoutAdjust(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))

	// With a zero balance, a payout over the balance is rejected and adjust 0 is rejected.
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/payout", map[string]any{"amount_cents": 100})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("payout over balance: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/adjust", map[string]any{"amount_cents": 0, "note": "无效"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("adjust zero: %d %s", rec.Code, rec.Body)
	}

	// adjust +500 books -> balance 500.
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/adjust", map[string]any{"amount_cents": 500, "note": "补偿"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("adjust: %d %s", rec.Code, rec.Body)
	}
	if got := mustJSON(t, rec)["balance_cents"].(float64); got != 500 {
		t.Fatalf("balance after adjust: %v", got)
	}

	// Payout 800 over the balance is rejected; 500 exactly -> balance 0.
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/payout", map[string]any{"amount_cents": 800})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("payout exceed: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/payout", map[string]any{"amount_cents": 500, "note": "10 月结算"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("payout: %d %s", rec.Code, rec.Body)
	}
	if got := mustJSON(t, rec)["balance_cents"].(float64); got != 0 {
		t.Fatalf("balance after payout: %v", got)
	}

	// Two ledger rows, plus the 404 surface.
	rec = doJSON(t, r, "GET", "/api/distributors/"+utoa(id)+"/ledger", nil)
	var led struct {
		Ledger       []map[string]any `json:"ledger"`
		BalanceCents float64          `json:"balance_cents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &led)
	if len(led.Ledger) != 2 || led.BalanceCents != 0 {
		t.Fatalf("ledger rows: %s", rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/distributors/999/payout", map[string]any{"amount_cents": 100})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("payout 404: %d %s", rec.Code, rec.Body)
	}
}

// TestPayoutAtomicGuard covers the atomic balance guard on payout booking
// (P1-5): when the precheck reads the old balance and another payout (on an
// independent connection, simulating a concurrent request) books first before
// the posting runs, the conditional INSERT lands 0 rows for insufficient
// balance and returns 400 with no new ledger row — the window between
// validation and booking can no longer overdraft. The router is built on a
// file-backed DB path so a second connection can act as the concurrent writer.
func TestPayoutAtomicGuard(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := storage.Open(storage.DriverSQLite, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	r, _ := NewRouter(db, config.Default())

	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d5", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))

	// adjust +500 books -> the precheck will see balance 500.
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/adjust", map[string]any{"amount_cents": 500})
	if rec.Code != http.StatusCreated {
		t.Fatalf("adjust: %d %s", rec.Code, rec.Body)
	}

	// Simulate concurrency: after the precheck aggregate read completes but
	// before the posting statement executes, another payout of 400 books
	// first. The hook hangs before the raw statement (ahead of the payout
	// conditional INSERT) and writes over an independent connection so the
	// posting statement sees it — an insert on the same connection would be
	// hidden by the driver snapshot, hence the second connection.
	raced := false
	db.Callback().Raw().Before("gorm:raw").Register("t:payoutrace", func(tx *gorm.DB) {
		if raced {
			return
		}
		raced = true
		conn2, err := storage.Open(storage.DriverSQLite, dbPath)
		if err != nil {
			t.Errorf("open racer conn: %v", err)
			return
		}
		if s, e := conn2.DB(); e == nil {
			defer func() { _ = s.Close() }()
		}
		if err := conn2.Exec(`INSERT INTO distributor_ledgers
			(distributor_id, kind, amount_cents, note, created_at)
			VALUES (?, 'payout', 400, 'concurrent', ?)`, id, time.Now()).Error; err != nil {
			t.Errorf("racer insert: %v", err)
		}
	})

	// Payout 500: the precheck passes at 500>=500, but only 100 remains at posting time -> the conditional INSERT lands 0 rows.
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/payout", map[string]any{"amount_cents": 500})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "exceeds unsettled balance") {
		t.Fatalf("lost race should 400: %d %s", rec.Code, rec.Body)
	}
	if !raced {
		t.Fatal("race hook never fired")
	}

	// Only the entry that cut in line posted; the balance is 100.
	var cnt int64
	db.Model(&storage.DistributorLedger{}).Where("distributor_id = ? AND kind = 'payout'", id).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("payout rows = %d, want 1 (only the racer)", cnt)
	}
	rec = doJSON(t, r, "GET", "/api/distributors/"+utoa(id)+"/ledger", nil)
	var led struct {
		BalanceCents float64 `json:"balance_cents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &led)
	if led.BalanceCents != 100 {
		t.Fatalf("balance = %v, want 100", led.BalanceCents)
	}
}

// TestDistributorSettlement pins the unified settlement status (F-1325):
// the ledger derives the state -- pending with open commission, settled once
// the payout empties the balance, overpaid defensively on a negative adjust;
// unknown ids 404.
func TestDistributorSettlement(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{
		"username": "settle-d", "password": "x12345", "discount_percent": 20,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create distributor: %d %s", rec.Code, rec.Body)
	}
	var d storage.Distributor
	if err := db.Where("username = ?", "settle-d").First(&d).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&storage.DistributorLedger{DistributorID: d.ID, OrderNo: "st-o1",
		Kind: "commission", AmountCents: 200, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	get := func() (string, int64, int64, int64, []map[string]any) {
		rec := doJSON(t, r, "GET", "/api/distributors/"+strconv.FormatUint(uint64(d.ID), 10)+"/settlement", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("settlement: %d %s", rec.Code, rec.Body)
		}
		var out struct {
			Status  string           `json:"status"`
			Earned  int64            `json:"earned_cents"`
			Paid    int64            `json:"paid_cents"`
			Open    int64            `json:"open_cents"`
			Entries []map[string]any `json:"entries"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Status, out.Earned, out.Paid, out.Open, out.Entries
	}

	status, earned, paid, open, entries := get()
	if status != "pending" || earned != 200 || paid != 0 || open != 200 || len(entries) != 1 {
		t.Fatalf("pending state: %s %d %d %d %+v", status, earned, paid, open, entries)
	}

	// Payout the full balance: settled.
	if rec := doJSON(t, r, "POST", "/api/distributors/"+strconv.FormatUint(uint64(d.ID), 10)+"/payout",
		map[string]any{"amount_cents": 200, "note": "bank"}); rec.Code != http.StatusCreated {
		t.Fatalf("payout: %d %s", rec.Code, rec.Body)
	}
	status, earned, paid, open, _ = get()
	if status != "settled" || earned != 200 || paid != 200 || open != 0 {
		t.Fatalf("settled state: %s %d %d %d", status, earned, paid, open)
	}

	// A negative adjust drives the balance negative: overpaid (defensive).
	if rec := doJSON(t, r, "POST", "/api/distributors/"+strconv.FormatUint(uint64(d.ID), 10)+"/adjust",
		map[string]any{"amount_cents": -50, "note": "correction"}); rec.Code != http.StatusCreated {
		t.Fatalf("adjust: %d %s", rec.Code, rec.Body)
	}
	status, _, _, open, _ = get()
	if status != "overpaid" || open != -50 {
		t.Fatalf("overpaid state: %s %d", status, open)
	}

	// Unknown distributor 404s.
	if rec := doJSON(t, r, "GET", "/api/distributors/424242/settlement", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
}
