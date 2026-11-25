package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch-30 gap fill: the user portal's (PAY-7) various 400/404/500 paths -
// token-resolution DB fault 500, the usage/device/IP count stages each
// failing, redemption bad binding/empty code/rate-limit lock/burn DB fault,
// order limit validation and the two-stage list query failures, the product
// list's two query failures (including the aggregate Scan that runs the
// gorm:row chain), order placement identity-missing/missing bind field/IP
// rate limit/batch-lookup DB fault/remaining count failure/campaign query
// failure/coupon burn failure/order persistence failure, order detail lookup
// DB fault and grant query failure, and the savings report DB fault.
// Left open: line 358's parseCampaignRules second-parse branch inside the
// transaction - bestCampaign has already parsed the same rules and skipped
// failing campaigns, so rules reaching the transaction must parse; the
// defensive code is unreachable.

// isPayOrderModel tells whether a statement targets the orders table.
func isPayOrderModel(v any) bool {
	switch v.(type) {
	case *storage.PaymentOrder, []storage.PaymentOrder, *[]storage.PaymentOrder:
		return true
	}
	return false
}

// isGrantModel tells whether a statement targets the grant records table.
func isGrantModel(v any) bool {
	switch v.(type) {
	case *storage.Grant, []storage.Grant, *[]storage.Grant:
		return true
	}
	return false
}

// isCardBatchModel tells whether a statement targets the card-batch table.
func isCardBatchModel(v any) bool {
	switch v.(type) {
	case *storage.CardBatch, []storage.CardBatch, *[]storage.CardBatch:
		return true
	}
	return false
}

// isCardCodeModel tells whether a statement targets the card-code table.
func isCardCodeModel(v any) bool {
	switch v.(type) {
	case *storage.CardCode, []storage.CardCode, *[]storage.CardCode:
		return true
	}
	return false
}

// isCampaignModel tells whether a statement targets the campaign table.
func isCampaignModel(v any) bool {
	switch v.(type) {
	case *storage.Campaign, []storage.Campaign, *[]storage.Campaign:
		return true
	}
	return false
}

// failQueryOn makes matching query statements fail (checking both Model and
// Dest, to cover the batch Find shape that carries only Dest).
func failQueryOn(t *testing.T, db *gorm.DB, name string, match func(any) bool) {
	t.Helper()
	db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if match(tx.Statement.Model) || match(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failRowOn makes matching Scan statements fail (aggregate Scan runs the
// gorm:row chain).
func failRowOn(t *testing.T, db *gorm.DB, name string, match func(any) bool) {
	t.Helper()
	db.Callback().Row().Before("gorm:row").Register(name, func(tx *gorm.DB) {
		if match(tx.Statement.Model) || match(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failUpdateOn makes matching update statements fail (aimed at card burn /
// coupon burn).
func failUpdateOn(t *testing.T, db *gorm.DB, name string, match func(any) bool) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if match(tx.Statement.Model) || match(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failCreateOn makes matching create statements fail (aimed at order persistence).
func failCreateOn(t *testing.T, db *gorm.DB, name string, match func(any) bool) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if match(tx.Statement.Model) || match(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// seedPanelUser writes one portal-accessible user row directly and returns its ID.
func seedPanelUser(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	row := storage.User{Username: name, SubToken: name + "-sub", QuotaBytes: 1 << 30, Enabled: true}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed panel user %s: %v", name, err)
	}
	return row.ID
}

// seedSellableBatch writes one sellable batch row directly (priced, in stock)
// and returns the batch ID.
func seedSellableBatch(t *testing.T, db *gorm.DB, name string, price int64) uint {
	t.Helper()
	batch := storage.CardBatch{Name: name, GrantType: "add_quota", GrantValue: 1 << 30,
		PriceCents: price, Total: 10, CreatedBy: "admin"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch %s: %v", name, err)
	}
	code := storage.CardCode{BatchID: batch.ID, Code: name + "-CODE", Status: "unused"}
	if err := db.Create(&code).Error; err != nil {
		t.Fatalf("seed code %s: %v", name, err)
	}
	return batch.ID
}

// TestPanelUserErrors covers token-resolution DB faults: non-not-found errors
// surface as 500 (the uniform 404 for missing/unmatched/disabled is covered
// by existing cases).
func TestPanelUserErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "pu")
	closeSQL(t, db)
	if rec := doPanel(t, r, "pu-sub", http.MethodGet, "/api/panel/me", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("db: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelMeErrors covers the usage/device/IP count stages each failing and
// surfacing 500.
func TestPanelMeErrors(t *testing.T) {
	// Usage Scan fails (the traffic table is dropped).
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "me-usage")
	if err := db.Migrator().DropTable(&storage.TrafficLog{}); err != nil {
		t.Fatalf("drop traffic: %v", err)
	}
	if rec := doPanel(t, r, "me-usage-sub", http.MethodGet, "/api/panel/me", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body)
	}

	// Device count fails (the device table is dropped).
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "me-dev")
	if err := db.Migrator().DropTable(&storage.SubDevice{}); err != nil {
		t.Fatalf("drop subdevice: %v", err)
	}
	if rec := doPanel(t, r, "me-dev-sub", http.MethodGet, "/api/panel/me", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("devices: %d %s", rec.Code, rec.Body)
	}

	// IP count fails: the device-stage count (1st) passes, error injection hits
	// the IP-stage count (2nd).
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "me-ip")
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:subdevice_at", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.SubDevice); ok {
			seen++
			if seen == 2 {
				tx.AddError(errors.New("db down"))
			}
		}
	})
	if rec := doPanel(t, r, "me-ip-sub", http.MethodGet, "/api/panel/me", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("ips: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelRedeemErrors covers redemption: bad binding, empty code, lockout
// after five straight failures, and a burn DB fault.
// redeemTx's atomic burn UPDATE runs first (executed even when no code is
// found; only 0 affected rows means the redemption failed), so injecting the
// UPDATE failure yields a non-redemption-failure 500.
func TestPanelRedeemErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "redeemer")
	if rec := doPanel(t, r, "redeemer-sub", http.MethodPost, "/api/panel/redeem", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, "redeemer-sub", http.MethodPost, "/api/panel/redeem",
		map[string]any{"code": "   "}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code: %d %s", rec.Code, rec.Body)
	}

	// Five straight failures lock it out: the sixth attempt is 429 directly (same
	// stance as dash-side redemption).
	for i := 0; i < 5; i++ {
		if rec := doPanel(t, r, "redeemer-sub", http.MethodPost, "/api/panel/redeem",
			map[string]any{"code": "NO-SUCH-CODE"}); rec.Code != http.StatusBadRequest {
			t.Fatalf("fail %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	if rec := doPanel(t, r, "redeemer-sub", http.MethodPost, "/api/panel/redeem",
		map[string]any{"code": "NO-SUCH-CODE"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked: %d %s", rec.Code, rec.Body)
	}

	// Burn UPDATE failure surfaces 500 (not the unified redemption-failure message).
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "redeemer2")
	failUpdateOn(t, db, "test:cardcode_update", func(v any) bool {
		_, ok := v.(*storage.CardCode)
		return ok
	})
	if rec := doPanel(t, r, "redeemer2-sub", http.MethodPost, "/api/panel/redeem",
		map[string]any{"code": "NO-SUCH-CODE"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("tx: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelOrdersErrors covers the order list: limit validation, order fetch
// failure, and grant query failure.
func TestPanelOrdersErrors(t *testing.T) {
	for _, limit := range []string{"abc", "0", "500"} {
		r, db := newTestRouterWithDB(t)
		seedPanelUser(t, db, "ord-lim-"+limit)
		if rec := doPanel(t, r, "ord-lim-"+limit+"-sub", http.MethodGet, "/api/panel/orders?limit="+limit, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%s: %d %s", limit, rec.Code, rec.Body)
		}
	}

	// A valid limit takes the clamping assignment branch and returns a 200 empty list.
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "ord-lim-ok")
	if rec := doPanel(t, r, "ord-lim-ok-sub", http.MethodGet, "/api/panel/orders?limit=5", nil); rec.Code != http.StatusOK {
		t.Fatalf("valid limit: %d %s", rec.Code, rec.Body)
	}

	// Order fetch fails.
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "ord-q")
	failQueryOn(t, db, "test:payorder_q", isPayOrderModel)
	if rec := doPanel(t, r, "ord-q-sub", http.MethodGet, "/api/panel/orders", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("orders: %d %s", rec.Code, rec.Body)
	}

	// Grant query fails (the list has an order, and the grant stage inside the
	// loop faults).
	r, db = newTestRouterWithDB(t)
	uid := seedPanelUser(t, db, "ord-g")
	if err := db.Create(&storage.PaymentOrder{OrderNo: "ORD-1", UserID: uid, Provider: "card",
		Product: "p", AmountCents: 100, Status: "paid"}).Error; err != nil {
		t.Fatal(err)
	}
	failQueryOn(t, db, "test:grant_q", isGrantModel)
	if rec := doPanel(t, r, "ord-g-sub", http.MethodGet, "/api/panel/orders", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("grants: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelProductsErrors covers the product list: missing identity 404, batch
// fetch failure, and remaining-stock aggregation failure (the aggregate Scan
// runs the gorm:row chain).
func TestPanelProductsErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doPanel(t, r, "", http.MethodGet, "/api/panel/products", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}

	// Batch fetch fails.
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "prod-b")
	failQueryOn(t, db, "test:cardbatch_q", isCardBatchModel)
	if rec := doPanel(t, r, "prod-b-sub", http.MethodGet, "/api/panel/products", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("batches: %d %s", rec.Code, rec.Body)
	}

	// Remaining-stock count fails (the Model is on the card-code table).
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "prod-r")
	failRowOn(t, db, "test:cardcode_row", isCardCodeModel)
	if rec := doPanel(t, r, "prod-r-sub", http.MethodGet, "/api/panel/products", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("remaining: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelCreateOrderErrors covers order placement: missing identity, missing
// bind fields, IP rate limiting, batch-lookup DB fault, remaining-count failure,
// campaign query failure, coupon burn failure, and order persistence failure.
// The gateway is taken before the batch lookup, so a fake gateway is
// registered first to let the run reach the DB fault stage.
func TestPanelCreateOrderErrors(t *testing.T) {
	// The gateway is taken before the batch lookup, so a fake gateway is
	// registered first to let the run reach the DB fault stage.
	payment.Register(stubGateway{})
	r, _ := newTestRouterWithDB(t)
	if rec := doPanel(t, r, "", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": 1, "provider": "epusdt"}); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, "", http.MethodPost, "/api/panel/orders", "not json"); rec.Code != http.StatusNotFound {
		t.Fatalf("bind no token: %d %s", rec.Code, rec.Body)
	}

	// Bad JSON binding and missing fields (with a token; missing batch_id takes
	// the same 400 message).
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-bind")
	if rec := doPanel(t, r, "co-bind-sub", http.MethodPost, "/api/panel/orders", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, "co-bind-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"provider": "epusdt"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing batch: %d %s", rec.Code, rec.Body)
	}

	// IP rate limiting: only 10 per minute are Allowed; the 11th gets 429 (the
	// count happens before the gateway too).
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-lim")
	var rec *httptest.ResponseRecorder
	for i := 0; i < 11; i++ {
		rec = doPanel(t, r, "co-lim-sub", http.MethodPost, "/api/panel/orders",
			map[string]any{"batch_id": 1, "provider": "epusdt"})
		if rec.Code == http.StatusTooManyRequests {
			break
		}
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: got %d after 11 attempts", rec.Code)
	}

	// Batch-lookup DB fault (the token query hits the User table, so it misses the
	// batch targeting).
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-loc")
	failQueryOn(t, db, "test:cardbatch_q2", isCardBatchModel)
	if rec := doPanel(t, r, "co-loc-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": 1, "provider": "epusdt"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("batch locate: %d %s", rec.Code, rec.Body)
	}

	// Remaining-count failure (the batch is sellable, and the count stage is the
	// one injected).
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-cnt")
	bid := seedSellableBatch(t, db, "co-cnt-b", 100)
	failQueryOn(t, db, "test:cardcode_q", isCardCodeModel)
	if rec := doPanel(t, r, "co-cnt-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": bid, "provider": "epusdt"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("count: %d %s", rec.Code, rec.Body)
	}

	// Campaign query fails (the first-stage query in bestCampaign is injected).
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-camp")
	bid = seedSellableBatch(t, db, "co-camp-b", 100)
	failQueryOn(t, db, "test:campaign_q", isCampaignModel)
	if rec := doPanel(t, r, "co-camp-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": bid, "provider": "epusdt"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("campaign: %d %s", rec.Code, rec.Body)
	}

	// Coupon burn fails: pre-check passed, the gateway order was created, the
	// conditional UPDATE inside the transaction is injected -> the whole
	// order rolls back with 500.
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-coup")
	bid = seedSellableBatch(t, db, "co-coup-b", 1000)
	start := time.Now().Add(-time.Hour)
	end := time.Now().Add(time.Hour)
	if err := db.Create(&storage.Coupon{Code: "SAVE10", Kind: "cut", Value: 100, Scope: "all",
		StartsAt: &start, EndsAt: &end, Total: 0, PerUser: 1}).Error; err != nil {
		t.Fatal(err)
	}
	failUpdateOn(t, db, "test:coupon_update", func(v any) bool {
		_, ok := v.(*storage.Coupon)
		return ok
	})
	if rec := doPanel(t, r, "co-coup-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": bid, "provider": "epusdt", "coupon_code": "save10"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("coupon burn: %d %s", rec.Code, rec.Body)
	}

	// Order persistence fails (the gateway order exists, the local transaction
	// is injected and rolls back).
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-tx")
	bid = seedSellableBatch(t, db, "co-tx-b", 100)
	failCreateOn(t, db, "test:payorder_create", isPayOrderModel)
	if rec := doPanel(t, r, "co-tx-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": bid, "provider": "epusdt"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("order create: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelOrderStatusErrors covers order detail: missing identity 404, lookup
// DB fault 500, and grant query failure 500.
func TestPanelOrderStatusErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doPanel(t, r, "", http.MethodGet, "/api/panel/orders/whatever", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}

	// Lookup DB fault (the order First after the token query is injected).
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "os-loc")
	failQueryOn(t, db, "test:payorder_q3", isPayOrderModel)
	if rec := doPanel(t, r, "os-loc-sub", http.MethodGet, "/api/panel/orders/whatever", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("locate: %d %s", rec.Code, rec.Body)
	}

	// Grant query fails.
	r, db = newTestRouterWithDB(t)
	uid := seedPanelUser(t, db, "os-g")
	if err := db.Create(&storage.PaymentOrder{OrderNo: "ORD-9", UserID: uid, Provider: "card",
		Product: "p", AmountCents: 100, Status: "paid"}).Error; err != nil {
		t.Fatal(err)
	}
	failQueryOn(t, db, "test:grant_q2", isGrantModel)
	if rec := doPanel(t, r, "os-g-sub", http.MethodGet, "/api/panel/orders/ORD-9", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("grants: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelSavingsError covers a savings-report DB fault (the first-stage
// save_stats query fails).
func TestPanelSavingsError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "sv-u")
	if err := db.Migrator().DropTable(&storage.SaveStat{}); err != nil {
		t.Fatalf("drop save_stats: %v", err)
	}
	if rec := doPanel(t, r, "sv-u-sub", http.MethodGet, "/api/panel/savings", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("savings: %d %s", rec.Code, rec.Body)
	}
}
