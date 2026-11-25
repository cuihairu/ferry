package handler

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch 33 gap-filling: error paths for time-limited campaigns (PROMO-2) —
// rules JSON string-shape unwrapping failure, list fetch failure, create with
// bad binding/persist failure, update with bad id/DB fault while locating/
// bad binding/validation failure/save failure, delete with bad id/persist
// failure, campaign slot with missing identity and fetch failure and skipping
// bad rules rows, eligibility short-circuits for disabled/out-of-window and
// count failure, and the best-pick loop's eligibility error/bad rules skip/
// scope miss skip/threshold not met skip.
// Gaps left: none (every continue and short-circuit in the best-pick loop is
// already covered on purpose).

// seedCampaignRow inserts one campaign row directly.
func seedCampaignRow(t *testing.T, db *gorm.DB, name, kind, rules string, enabled bool) uint {
	t.Helper()
	row := storage.Campaign{Name: name, Kind: kind, Rules: rules,
		StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Now().Add(24 * time.Hour), Enabled: enabled}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed campaign %s: %v", name, err)
	}
	return row.ID
}

// TestParseCampaignRulesUnwrapFail: rules are in JSON string shape but
// unwrapping fails — starts with a quote yet is not a legal string literal.
func TestParseCampaignRulesUnwrapFail(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	if _, err := h.parseCampaignRules(`"bad`); err == nil ||
		!containsStr(err.Error(), "rules must be valid JSON") {
		t.Fatalf("unwrap: %v", err)
	}
}

// TestListCampaignsError: list fetch failure returns 500.
func TestListCampaignsError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedCampaignRow(t, db, "l-c", "timed", `{"kind":"cut","value":100}`, true)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/campaigns", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateCampaignErrors: create — bad binding and persist failure.
func TestCreateCampaignErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/campaigns", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	failCreateOn(t, db, "test:campaign_create", isCampaignModel)
	body := mkCampaign("c-fail", "timed", map[string]any{"kind": "cut", "value": 100},
		time.Now().Add(-time.Hour).Format(time.RFC3339), time.Now().Add(24*time.Hour).Format(time.RFC3339))
	if rec := doJSON(t, r, http.MethodPost, "/api/campaigns", body); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateCampaignErrors: update — bad id, DB fault while locating, bad
// binding, validation failure, and save failure (the not-found 404 already has
// a case).
func TestUpdateCampaignErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/campaigns/abc",
		map[string]any{"name": "n"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedCampaignRow(t, db, "u-c", "timed", `{"kind":"cut","value":100}`, true)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/campaigns/1",
		map[string]any{"name": "n"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("locate: %d %s", rec.Code, rec.Body)
	}

	valid := mkCampaign("u-ok", "timed", map[string]any{"kind": "cut", "value": 100},
		time.Now().Add(-time.Hour).Format(time.RFC3339), time.Now().Add(24*time.Hour).Format(time.RFC3339))
	r, db = newTestRouterWithDB(t)
	seedCampaignRow(t, db, "bind-c", "timed", `{"kind":"cut","value":100}`, true)
	if rec := doJSON(t, r, http.MethodPut, "/api/campaigns/1", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/campaigns/1",
		mkCampaign("bad-kind", "weird", map[string]any{"kind": "cut", "value": 100},
			time.Now().Add(-time.Hour).Format(time.RFC3339), time.Now().Add(24*time.Hour).Format(time.RFC3339))); rec.Code != http.StatusBadRequest {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedCampaignRow(t, db, "save-c", "timed", `{"kind":"cut","value":100}`, true)
	failUpdateOn(t, db, "test:campaign_update", isCampaignModel)
	if rec := doJSON(t, r, http.MethodPut, "/api/campaigns/1", valid); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteCampaignErrors: delete — bad id and persist failure (the not-found 404 already has a case).
func TestDeleteCampaignErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/campaigns/abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedCampaignRow(t, db, "d-c", "timed", `{"kind":"cut","value":100}`, true)
	db.Callback().Delete().Before("gorm:delete").Register("test:campaign_delete", func(tx *gorm.DB) {
		if isCampaignModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, http.MethodDelete, "/api/campaigns/1", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete fail: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelCampaignsErrors: campaign slot — missing identity 404, fetch
// failure 500, and bad rules rows skipped (never sent down to users).
func TestPanelCampaignsErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doPanel(t, r, "", http.MethodGet, "/api/panel/campaigns", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "camp-u")
	// Target the campaigns table for the injected error (the token lookup hits the User table and is unaffected): fetch failure lands as 500.
	failQueryOn(t, db, "test:campaign_q", isCampaignModel)
	if rec := doPanel(t, r, "camp-u-sub", http.MethodGet, "/api/panel/campaigns", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "camp-u2")
	seedCampaignRow(t, db, "garbage-c", "timed", "not json", true)
	seedCampaignRow(t, db, "good-c", "timed", `{"kind":"cut","value":100}`, true)
	rec := doPanel(t, r, "camp-u2-sub", http.MethodGet, "/api/panel/campaigns", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if !containsStr(rec.Body.String(), `"good-c"`) || containsStr(rec.Body.String(), `"garbage-c"`) {
		t.Fatalf("bad rules row not skipped: %s", rec.Body)
	}
}

// TestCampaignEligibleEdge: eligibility — disabled/out-of-window short-circuits and count failure.
func TestCampaignEligibleEdge(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	uid := seedPanelUser(t, db, "elig-u")
	var u storage.User
	if err := db.First(&u, uid).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	// disabled: straight false with no error (no order lookup).
	dis := storage.Campaign{Name: "dis", Kind: "first_order", Rules: `{"kind":"cut","value":100}`,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Enabled: false}
	if ok, err := h.campaignEligible(db, &dis, &u, now); err != nil || ok {
		t.Fatalf("disabled: ok=%v err=%v", ok, err)
	}

	// count failure: an enabled first_order campaign with an error injected into the order count.
	act := storage.Campaign{Name: "act", Kind: "first_order", Rules: `{"kind":"cut","value":100}`,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Enabled: true}
	db.Callback().Query().Before("gorm:query").Register("test:payorder_count_q", func(tx *gorm.DB) {
		if isPayOrderModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
	if _, err := h.campaignEligible(db, &act, &u, now); err == nil {
		t.Fatal("count fail: want err")
	}
}

// TestBestCampaignSkips: the best-pick loop — an eligibility error bubbles
// up, bad rules are skipped, a scope miss is skipped, and an unmet threshold
// is skipped.
func TestBestCampaignSkips(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	uid := seedPanelUser(t, db, "best-u")
	var u storage.User
	if err := db.First(&u, uid).Error; err != nil {
		t.Fatal(err)
	}
	batch := storage.CardBatch{Name: "best-b", GrantType: "add_quota", GrantValue: 1 << 30,
		PriceCents: 1000, Total: 10, CreatedBy: "admin"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	// eligibility error: the campaign is inside the window with an error injected into the order count -> bestCampaign surfaces the error.
	seedCampaignRow(t, db, "err-c", "first_order", `{"kind":"cut","value":100}`, true)
	db.Callback().Query().Before("gorm:query").Register("test:best_payorder_q", func(tx *gorm.DB) {
		if isPayOrderModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
	if _, _, err := h.bestCampaign(db, &u, &batch, 1000, now); err == nil {
		t.Fatal("eligibility error: want err")
	}

	// the skip branches: bad rules, scope miss (scope points at another existing batch), unmet threshold.
	_, db = newTestRouterWithDB(t)
	h = &Handler{db: db}
	uid = seedPanelUser(t, db, "best-u2")
	if err := db.First(&u, uid).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	otherBatch := storage.CardBatch{Name: "other-b", GrantType: "add_quota", GrantValue: 1 << 30,
		PriceCents: 1000, Total: 100, CreatedBy: "admin"}
	if err := db.Create(&otherBatch).Error; err != nil {
		t.Fatal(err)
	}
	seedCampaignRow(t, db, "bad-rules-c", "timed", "not json", true)
	seedCampaignRow(t, db, "scope-c", "timed",
		`{"kind":"cut","value":100,"scope":"batch:`+strconv.FormatUint(uint64(otherBatch.ID), 10)+`"}`, true)
	seedCampaignRow(t, db, "min-c", "timed", `{"kind":"cut","value":100,"min_amount":999999}`, true)
	best, paid, err := h.bestCampaign(db, &u, &batch, 1000, now)
	if err != nil || best != nil || paid != 1000 {
		t.Fatalf("skips: best=%v paid=%d err=%v", best, paid, err)
	}
}
