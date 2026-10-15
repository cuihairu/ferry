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

// 三十三批补口：限时活动（PROMO-2）错误口径——规则 JSON 字符串形态解包
// 失败、列表取数失败、建号坏绑定/落库失败、更新坏 id/库故障定位/坏绑定/
// 校验失败/保存失败、删除坏 id/落删失败、活动位身份缺失与取数失败与坏规
// 则行跳过、资格判定的停用/出窗短路与计数失败、取优循环的资格报错/坏规
// 则跳过/范围不中跳过/门槛不达跳过。
// 留白：无（取优循环各 continue 与短路均已定向覆盖）。

// seedCampaignRow 直落一行活动。
func seedCampaignRow(t *testing.T, db *gorm.DB, name, kind, rules string, enabled bool) uint {
	t.Helper()
	row := storage.Campaign{Name: name, Kind: kind, Rules: rules,
		StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Now().Add(24 * time.Hour), Enabled: enabled}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed campaign %s: %v", name, err)
	}
	return row.ID
}

// TestParseCampaignRulesUnwrapFail 规则为 JSON 字符串形态但解包失败：带引
// 号开头却不是合法字符串字面量。
func TestParseCampaignRulesUnwrapFail(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	if _, err := h.parseCampaignRules(`"bad`); err == nil ||
		!containsStr(err.Error(), "rules must be valid JSON") {
		t.Fatalf("unwrap: %v", err)
	}
}

// TestListCampaignsError 列表取数失败 500。
func TestListCampaignsError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedCampaignRow(t, db, "l-c", "timed", `{"kind":"cut","value":100}`, true)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/campaigns", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateCampaignErrors 建号：坏绑定与落库失败。
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

// TestUpdateCampaignErrors 更新：坏 id、库故障定位、坏绑定、校验失败与保
// 存失败（查无 404 已有用例）。
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

// TestDeleteCampaignErrors 删除：坏 id 与落删失败（查无 404 已有用例）。
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

// TestPanelCampaignsErrors 活动位：身份缺失 404、取数失败 500 与坏规则行
// 跳过（不下发给用户）。
func TestPanelCampaignsErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doPanel(t, r, "", http.MethodGet, "/api/panel/campaigns", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "camp-u")
	// 定向活动表注错（令牌查用户在 User 表，不受影响）：取数失败落 500。
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

// TestCampaignEligibleEdge 资格判定：停用/出窗短路与计数失败。
func TestCampaignEligibleEdge(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	uid := seedPanelUser(t, db, "elig-u")
	var u storage.User
	if err := db.First(&u, uid).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	// 停用：直接 false 无错（不查订单）。
	dis := storage.Campaign{Name: "dis", Kind: "first_order", Rules: `{"kind":"cut","value":100}`,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Enabled: false}
	if ok, err := h.campaignEligible(db, &dis, &u, now); err != nil || ok {
		t.Fatalf("disabled: ok=%v err=%v", ok, err)
	}

	// 计数失败：启用的首单活动，订单计数注错。
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

// TestBestCampaignSkips 取优循环：资格报错冒出、坏规则跳过、范围不中跳
// 过与门槛不达跳过。
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

	// 资格报错：活动在窗内，订单计数注错 → bestCampaign 冒错。
	seedCampaignRow(t, db, "err-c", "first_order", `{"kind":"cut","value":100}`, true)
	db.Callback().Query().Before("gorm:query").Register("test:best_payorder_q", func(tx *gorm.DB) {
		if isPayOrderModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
	if _, _, err := h.bestCampaign(db, &u, &batch, 1000, now); err == nil {
		t.Fatal("eligibility error: want err")
	}

	// 各跳过分支：坏规则、范围不中（scope 指向另一张存在的批次）、门槛不达。
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
