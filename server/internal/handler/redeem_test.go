package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// redeemCreate creates a user and a card batch, returning the user id and the
// batch's code list.
func redeemCreate(t *testing.T, r *gin.Engine, username string, grant map[string]any, total int) (uint, []string) {
	t.Helper()
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": username, "quota_bytes": 1000})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	userID := uint(u["id"].(float64))

	body := map[string]any{"name": "测试批次", "total": total}
	for k, v := range grant {
		body[k] = v
	}
	rec = doJSON(t, r, "POST", "/api/card-batches", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create batch: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		Codes []string `json:"codes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	return userID, created.Codes
}

func TestRedeemQuota(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "alice", map[string]any{
		"grant_type": "add_quota", "grant_value": 5 << 30,
	}, 2)

	// redeem: quota 1000 + 5GiB
	rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
	var res struct {
		OrderNo    string `json:"order_no"`
		GrantType  string `json:"grant_type"`
		QuotaBytes int64  `json:"quota_bytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.GrantType != "add_quota" || res.QuotaBytes != 1000+5*1<<30 || res.OrderNo == "" {
		t.Fatalf("result = %+v", res)
	}

	// all three ledgers populated
	var orders int64
	db.Model(&storage.PaymentOrder{}).Count(&orders)
	var txns int64
	db.Model(&storage.PaymentTransaction{}).Count(&txns)
	var grants int64
	db.Model(&storage.Grant{}).Count(&grants)
	if orders != 1 || txns != 1 || grants != 1 {
		t.Fatalf("ledgers: orders=%d txns=%d grants=%d", orders, txns, grants)
	}

	// replaying the same code: 400 with the unified message, and the code row's
	// fail_count increments
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID})
	if rec.Code != http.StatusBadRequest || rec.Body.String() != `{"error":"兑换失败"}` {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body)
	}
	var codeRow storage.CardCode
	if err := db.Where("code = ?", codes[0]).First(&codeRow).Error; err != nil {
		t.Fatal(err)
	}
	if codeRow.FailCount != 1 || codeRow.Status != "used" {
		t.Fatalf("code after replay = %+v", codeRow)
	}
	// no new ledger rows
	db.Model(&storage.PaymentOrder{}).Count(&orders)
	if orders != 1 {
		t.Fatalf("replay created order: %d", orders)
	}
}

func TestRedeemExtendDays(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "bob", map[string]any{
		"grant_type": "extend_days", "grant_value": 30,
	}, 1)
	// bob has no expiry -> +30 days counted from now
	rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/users/"+fmt.Sprint(userID), nil)
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	exp, _ := time.Parse(time.RFC3339, u["expires_at"].(string))
	if d := time.Until(exp); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("expires_at = %v (until %v)", u["expires_at"], d)
	}
}

func TestRedeemFailureModes(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "carol", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 1)

	// unknown code: unified message
	rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": "XXXX-XXXX-XXXX", "user_id": userID})
	if rec.Code != http.StatusBadRequest || rec.Body.String() != `{"error":"兑换失败"}` {
		t.Fatalf("unknown code: %d %s", rec.Code, rec.Body)
	}
	// unknown user: unified message (anti-enumeration)
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": 99})
	if rec.Code != http.StatusBadRequest || rec.Body.String() != `{"error":"兑换失败"}` {
		t.Fatalf("unknown user: %d %s", rec.Code, rec.Body)
	}
	// unknown-user failures do not harm the card: the code stays redeemable
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("code should still redeem: %d %s", rec.Code, rec.Body)
	}

	// expired batch: set the batch expiry to the past after creation (simulating
	// the passage of time); redemption must fail and roll back the write-off
	user2, codes2 := redeemCreate(t, r, "dave", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 1)
	past := time.Now().Add(-time.Hour)
	if err := db.Model(&storage.CardBatch{}).Where("id = 2").Update("expired_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes2[0], "user_id": user2}); rec.Code != http.StatusBadRequest {
		t.Fatalf("expired batch: %d", rec.Code)
	}
	var row storage.CardCode
	if err := db.Where("code = ?", codes2[0]).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "unused" || row.UsedBy != nil {
		t.Fatalf("expired redeem should roll back claim: %+v", row)
	}
}

// doJSONFrom sends the request from a given source IP (rate limiting is per IP,
// so different IPs isolate scenarios).
func doJSONFrom(t *testing.T, r *gin.Engine, ip, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.RemoteAddr = ip + ":1234"
	req.Header.Set("Content-Type", "application/json")
	// /api/redeem sits on the admin data plane (apiAuth): like doJSON it carries
	// the admin JWT.
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRedeemDisabledByFails(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "erin", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 2)
	// P2 fix: repeated bad-user attempts (invalid user_id) against an unused
	// code no longer accrue failures or disable it — the failure is not caused
	// by the card itself, so the card is not blamed (anti-malicious-disable).
	for i := 0; i < 5; i++ {
		rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[1], "user_id": 99})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	var row storage.CardCode
	if err := db.Where("code = ?", codes[1]).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "unused" || row.FailCount != 0 {
		t.Fatalf("bad-user failure must not disable/penalize unused card: %+v", row)
	}

	// different IP: redeem codes[0] normally, then replay. A used code (the card
	// itself unusable) only accrues counts without changing status (used
	// semantics preserved).
	if rec := doJSONFrom(t, r, "203.0.113.9", "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID}); rec.Code != http.StatusOK {
		t.Fatalf("fresh-ip redeem: %d %s", rec.Code, rec.Body)
	}
	for i := 0; i < 5; i++ {
		if rec := doJSONFrom(t, r, "203.0.113.9", "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID}); rec.Code != http.StatusBadRequest {
			t.Fatalf("replay %d: %d", i, rec.Code)
		}
	}
	var used storage.CardCode
	if err := db.Where("code = ?", codes[0]).First(&used).Error; err != nil {
		t.Fatal(err)
	}
	if used.Status != "used" || used.FailCount < 5 {
		t.Fatalf("used code keeps status, counts fails: %+v", used)
	}
}

// TestRedeemBadUserDoesNotDisableCard covers the P2 fix for "failed redemptions
// can maliciously disable a card": repeatedly submitting a usable unused card
// code with an invalid user_id (each write-off rolls back) no longer accrues
// fail_count or auto-disables it — before the fix, 5 invalid user_id attempts
// would mark a usable card disabled right in front of its legitimate holder.
// After the fix the card stays unused with a zero count and still redeems
// normally.
func TestRedeemBadUserDoesNotDisableCard(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "grief", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 1)
	// 5 invalid user_id attempts (the malicious-disable technique): unified
	// failure message, card not disabled.
	for i := 0; i < 5; i++ {
		if rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": 9999}); rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	var row storage.CardCode
	if err := db.Where("code = ?", codes[0]).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "unused" || row.FailCount != 0 {
		t.Fatalf("card should stay unused, unpenalized: %+v", row)
	}
	// the legitimate user can still redeem (no malicious disable); switch IP to
	// dodge this IP's failure lock (5 failures already lock it for 15 minutes;
	// rate-limit conventions unchanged).
	if rec := doJSONFrom(t, r, "198.51.100.77", "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID}); rec.Code != http.StatusOK {
		t.Fatalf("card should still redeem: %d %s", rec.Code, rec.Body)
	}
}

func TestDisableCardCode(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "grace", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 2)

	var first storage.CardCode
	if err := db.Where("code = ?", codes[0]).First(&first).Error; err != nil {
		t.Fatal(err)
	}
	// disable an unused code: succeeds and persists the status
	rec := doJSON(t, r, "PATCH", "/api/card-codes/"+fmt.Sprint(first.ID)+"/disable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	var after storage.CardCode
	db.Where("code = ?", codes[0]).First(&after)
	if after.Status != "disabled" {
		t.Fatalf("status = %s", after.Status)
	}
	// a disabled code cannot be redeemed; redemption returns the unified message
	if rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID}); rec.Code != http.StatusBadRequest {
		t.Fatalf("redeem disabled: %d", rec.Code)
	}
	// disabling again (disabled is terminal): 409
	if rec := doJSON(t, r, "PATCH", "/api/card-codes/"+fmt.Sprint(first.ID)+"/disable", nil); rec.Code != http.StatusConflict {
		t.Fatalf("re-disable: %d %s", rec.Code, rec.Body)
	}
	// a used code cannot be disabled: 409
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[1], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
	var second storage.CardCode
	db.Where("code = ?", codes[1]).First(&second)
	if rec := doJSON(t, r, "PATCH", "/api/card-codes/"+fmt.Sprint(second.ID)+"/disable", nil); rec.Code != http.StatusConflict {
		t.Fatalf("disable used: %d %s", rec.Code, rec.Body)
	}
	// unknown id: also 409 like an undisableable one (anti-enumeration
	// convention, see card_test.go)
	if rec := doJSON(t, r, "PATCH", "/api/card-codes/999/disable", nil); rec.Code != http.StatusConflict {
		t.Fatalf("missing: %d", rec.Code)
	}
}

// TestRedeemEdges covers redemption negative cases and copy boundaries: bad
// JSON/empty code/missing user all give the 400 unified message, MB-scale grant
// notification copy, and duration extension for a user with an expiry counting
// from the existing expiry.
func TestRedeemEdges(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	userID, _ := redeemCreate(t, r, "edge-a", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 1)

	// bad JSON -> 400 unified message (no parse details leaked).
	if rec := doRaw(t, r, "POST", "/api/redeem", "{bad"); rec.Code != http.StatusBadRequest || rec.Body.String() != `{"error":"兑换失败"}` {
		t.Fatalf("bad json: %d %s", rec.Code, rec.Body)
	}
	// empty code / missing user -> 400.
	if rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": "", "user_id": userID}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": "AAAA-BBBB-CCCC"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("no user: %d", rec.Code)
	}

	// MB-scale copy: a 300MB quota grant -> in-app message title counts in MB.
	r2, db2 := newTestRouterWithDB(t)
	user2, codes2 := redeemCreate(t, r2, "edge-b", map[string]any{
		"grant_type": "add_quota", "grant_value": 300 << 20,
	}, 1)
	if rec := doJSON(t, r2, "POST", "/api/redeem", map[string]any{"code": codes2[0], "user_id": user2}); rec.Code != http.StatusOK {
		t.Fatalf("mb redeem: %d %s", rec.Code, rec.Body)
	}
	var note storage.Notification
	if err := db2.Where("user_id = ?", user2).First(&note).Error; err != nil {
		t.Fatal(err)
	}
	if note.Title != "流量已到账：+300 MB" {
		t.Fatalf("notification title = %q", note.Title)
	}

	// user with an expiry: duration extension counts from the existing expiry
	// (+10 days then +30 -> about +40 days).
	r3, db3 := newTestRouterWithDB(t)
	user3, codes3 := redeemCreate(t, r3, "edge-c", map[string]any{
		"grant_type": "extend_days", "grant_value": 30,
	}, 1)
	if err := db3.Model(&storage.User{}).Where("id = ?", user3).
		Update("expires_at", time.Now().Add(10*24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r3, "POST", "/api/redeem", map[string]any{"code": codes3[0], "user_id": user3}); rec.Code != http.StatusOK {
		t.Fatalf("extend redeem: %d %s", rec.Code, rec.Body)
	}
	var eu storage.User
	if err := db3.First(&eu, user3).Error; err != nil {
		t.Fatal(err)
	}
	if d := time.Until(*eu.ExpiresAt); d < 39*24*time.Hour || d > 41*24*time.Hour {
		t.Fatalf("expires_at until = %v, want ~40d", d)
	}
}

// TestRedeemDBErrors covers read/booking failures at each step of the redemption
// transaction -> 500 with a full rollback and zero writes (gorm callback error
// injection, matching the existing convention); it also includes two more 500s:
// a deleted batch and a bad distributor reference.
func TestRedeemDBErrors(t *testing.T) {
	scenarios := []struct {
		name string
		reg  func(db *gorm.DB)
	}{
		{"order create", func(db *gorm.DB) {
			db.Callback().Create().Before("gorm:create").Register("t:order", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.PaymentOrder); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"ledger create", func(db *gorm.DB) {
			db.Callback().Create().Before("gorm:create").Register("t:ledger", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*[]storage.DistributorLedger); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"txn create", func(db *gorm.DB) {
			db.Callback().Create().Before("gorm:create").Register("t:txn", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.PaymentTransaction); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"grant create", func(db *gorm.DB) {
			db.Callback().Create().Before("gorm:create").Register("t:grant", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.Grant); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"notification create", func(db *gorm.DB) {
			db.Callback().Create().Before("gorm:create").Register("t:note", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.Notification); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"user update", func(db *gorm.DB) {
			db.Callback().Update().Before("gorm:update").Register("t:userupd", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Model.(*storage.User); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"code read", func(db *gorm.DB) {
			db.Callback().Query().Before("gorm:query").Register("t:codeload", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.CardCode); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"user read", func(db *gorm.DB) {
			db.Callback().Query().Before("gorm:query").Register("t:userload", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.User); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"batch read", func(db *gorm.DB) {
			db.Callback().Query().Before("gorm:query").Register("t:batchload", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.CardBatch); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"distributor missing", nil},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			r, db := newTestRouterWithDB(t)
			userID, codes := redeemCreate(t, r, "dbx", map[string]any{
				"grant_type": "add_quota", "grant_value": 1 << 30,
			}, 1)
			var codeRow storage.CardCode
			if err := db.Where("code = ?", codes[0]).First(&codeRow).Error; err != nil {
				t.Fatal(err)
			}
			switch sc.name {
			case "ledger create":
				// real distributor: the batch carries a distributor with a listed
				// price, and the commission row books as usual.
				rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{
					"username": "dbx-dist", "password": "secret123", "discount_percent": 30,
				})
				if rec.Code != http.StatusCreated {
					t.Fatalf("create distributor: %d %s", rec.Code, rec.Body)
				}
				var created struct {
					Distributor map[string]any `json:"distributor"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &created)
				if err := db.Model(&storage.CardBatch{}).Where("id = ?", codeRow.BatchID).
					Updates(map[string]any{
						"distributor_id": uint(created.Distributor["id"].(float64)),
						"price_cents":    100,
					}).Error; err != nil {
					t.Fatal(err)
				}
			case "distributor missing":
				if err := db.Model(&storage.CardBatch{}).Where("id = ?", codeRow.BatchID).
					Updates(map[string]any{"distributor_id": 999, "price_cents": 100}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if sc.reg != nil {
				sc.reg(db)
			}
			rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID})
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
			}
			// transactional rollback, zero writes: no order, no grant.
			var orders, grants int64
			db.Model(&storage.PaymentOrder{}).Count(&orders)
			db.Model(&storage.Grant{}).Count(&grants)
			if orders != 0 || grants != 0 {
				t.Fatalf("leaked rows: orders=%d grants=%d", orders, grants)
			}
		})
	}
}

func TestRedeemRateLimited(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	userID, _ := redeemCreate(t, r, "frank", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 1)
	// 10 consecutive attempts fill the window; the 11th gets 429
	sawRateLimit := false
	for i := 0; i < 11; i++ {
		rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": "AAAA-BBBB-CCCC", "user_id": userID})
		if rec.Code == http.StatusTooManyRequests {
			sawRateLimit = true
			break
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if !sawRateLimit {
		t.Fatal("expected 429 within 11 attempts")
	}
}
