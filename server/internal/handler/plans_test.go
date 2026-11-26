package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPlanCRUD covers the package-template surface (F-0101): create echoes the
// normalized row, a duplicate name conflicts, invalid limits fail, update
// merges absent fields, and delete removes the row.
func TestPlanCRUD(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	body := map[string]any{
		"name": "basic", "note": "entry package",
		"quota_bytes": 10 << 30, "reset_cycle": "month",
		"expires_days": 30, "bw_up_mbps": 3, "bw_down_mbps": 5,
		"device_limit": 2, "ip_limit": 3,
	}
	rec := doJSON(t, r, "POST", "/api/plans", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var p storage.Plan
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.ID == 0 {
		t.Fatalf("created row = %s", rec.Body)
	}
	if p.QuotaBytes != 10<<30 || p.ResetCycle != "month" || p.ExpiresDays != 30 ||
		p.BwUpMbps != 3 || p.BwDownMbps != 5 || p.DeviceLimit != 2 || p.IPLimit != 3 || !p.Enabled {
		t.Fatalf("created row = %+v", p)
	}

	// Duplicate name conflicts.
	if rec = doJSON(t, r, "POST", "/api/plans", map[string]any{"name": "basic"}); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name = %d", rec.Code)
	}
	// Validation failures carry 400.
	if rec = doJSON(t, r, "POST", "/api/plans", map[string]any{"name": "bad", "reset_cycle": "yearly"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cycle = %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/plans", map[string]any{"name": "bad", "bw_up_mbps": -1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative bw = %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/plans", map[string]any{"name": ""}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name = %d", rec.Code)
	}

	// Update merges absent fields (quota/cycle stay), writes present ones.
	rec = doJSON(t, r, "PUT", "/api/plans/"+utoa(p.ID), map[string]any{
		"name": "basic-2", "device_limit": 4, "enabled": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var up storage.Plan
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	if up.Name != "basic-2" || up.DeviceLimit != 4 || up.Enabled ||
		up.QuotaBytes != 10<<30 || up.ResetCycle != "month" || up.Note != "entry package" {
		t.Fatalf("updated row = %+v", up)
	}

	// Rename onto another plan's name conflicts.
	rec = doJSON(t, r, "POST", "/api/plans", map[string]any{"name": "pro"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("second create: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "PUT", "/api/plans/"+utoa(p.ID), map[string]any{"name": "pro"}); rec.Code != http.StatusConflict {
		t.Fatalf("rename conflict = %d", rec.Code)
	}

	// List and get.
	if rec = doJSON(t, r, "GET", "/api/plans", nil); rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	if rec = doJSON(t, r, "GET", "/api/plans/"+utoa(p.ID), nil); rec.Code != http.StatusOK {
		t.Fatalf("get: %d", rec.Code)
	}
	if rec = doJSON(t, r, "GET", "/api/plans/999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing get = %d", rec.Code)
	}

	// Delete: second delete 404s.
	if rec = doJSON(t, r, "DELETE", "/api/plans/"+utoa(p.ID), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec = doJSON(t, r, "DELETE", "/api/plans/"+utoa(p.ID), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("re-delete = %d", rec.Code)
	}
	var n int64
	db.Model(&storage.Plan{}).Where("id = ?", p.ID).Count(&n)
	if n != 0 {
		t.Fatalf("plan row survived delete")
	}
}

// TestPlanApplyUsers covers applying a package to users: limits copy over,
// expiry is set relative to now when expires_days > 0, unknown users come back
// as failed rows, and a disabled plan refuses to apply.
func TestPlanApplyUsers(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/plans", map[string]any{
		"name": "pkg", "quota_bytes": 5 << 30, "reset_cycle": "week",
		"expires_days": 7, "bw_up_mbps": 3, "bw_down_mbps": 5,
		"device_limit": 2, "ip_limit": 2,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plan: %d %s", rec.Code, rec.Body)
	}
	var plan storage.Plan
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}

	uid := func(name string) float64 {
		rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": name, "password": "pass-123"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create user %s: %d %s", name, rec.Code, rec.Body)
		}
		return mustJSON(t, rec)["id"].(float64)
	}
	u1, u2 := uid("plan-u1"), uid("plan-u2")

	// Partial failure: one unknown id in the middle must not fail the rest.
	rec = doJSON(t, r, "POST", "/api/plans/"+utoa(plan.ID)+"/apply", map[string]any{
		"user_ids": []any{u1, 9999, u2},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Results []struct {
			UserID uint   `json:"user_id"`
			OK     bool   `json:"ok"`
			Error  string `json:"error,omitempty"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 3 || !out.Results[0].OK || out.Results[1].OK || out.Results[1].Error != "user not found" || !out.Results[2].OK {
		t.Fatalf("results = %+v", out.Results)
	}

	before := time.Now()
	for _, id := range []uint{uint(u1), uint(u2)} {
		var u storage.User
		if err := db.First(&u, id).Error; err != nil {
			t.Fatal(err)
		}
		if u.QuotaBytes != 5<<30 || u.ResetCycle != "week" || u.BwUpMbps != 3 ||
			u.BwDownMbps != 5 || u.DeviceLimit != 2 || u.IPLimit != 2 {
			t.Fatalf("user %d after apply = %+v", id, u)
		}
		if u.ExpiresAt == nil {
			t.Fatalf("user %d expiry not set", id)
		}
		days := u.ExpiresAt.Sub(before).Hours() / 24
		if days < 6.9 || days > 7.1 {
			t.Fatalf("user %d expiry = %v (want ~7 days)", id, u.ExpiresAt)
		}
	}

	// Missing plan and empty body 404/400 (plan still enabled here).
	if rec = doJSON(t, r, "POST", "/api/plans/999/apply", map[string]any{"user_ids": []any{u1}}); rec.Code != http.StatusNotFound {
		t.Fatalf("apply on missing plan = %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/plans/"+utoa(plan.ID)+"/apply", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty apply = %d", rec.Code)
	}

	// A disabled plan refuses to apply.
	if rec = doJSON(t, r, "PUT", "/api/plans/"+utoa(plan.ID), map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("disable plan: %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/plans/"+utoa(plan.ID)+"/apply", map[string]any{
		"user_ids": []any{u1},
	}); rec.Code != http.StatusConflict {
		t.Fatalf("apply on disabled plan = %d %s", rec.Code, rec.Body)
	}
}

// TestPlanExpiryRules pins the plan activation/expiry rules (F-1203): an apply
// with expires_days=0 copies the limits but leaves the user's expiry untouched;
// the user-level expiry then governs delivery on its own -- an expired user's
// subscription goes empty, and extending ExpiresAt restores delivery with the
// applied limits still in place (no re-apply needed).
func TestPlanExpiryRules(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	tok := panelUserOf(t, r, "expiry-u", map[string]any{"expires_at": "2030-01-01T00:00:00Z"})
	if err := db.Create(&storage.Node{Name: "hk", Address: "a.example.com", Port: 443,
		Protocol: "vless", Config: `{"uuid":"u"}`, Enabled: true,
		Token: "tok-expiry", Role: "entry", Status: "online"}).Error; err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, r, "POST", "/api/plans", map[string]any{
		"name": "expiry-pkg", "quota_bytes": 1 << 30, "expires_days": 0,
		"bw_up_mbps": 3, "bw_down_mbps": 5,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plan: %d %s", rec.Code, rec.Body)
	}
	var plan storage.Plan
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	var u storage.User
	if err := db.Where("username = ?", "expiry-u").First(&u).Error; err != nil {
		t.Fatal(err)
	}

	// expires_days=0: limits copy over, expiry stays 2030.
	if rec = doJSON(t, r, "POST", "/api/plans/"+utoa(plan.ID)+"/apply", map[string]any{
		"user_ids": []any{u.ID},
	}); rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	var after storage.User
	if err := db.First(&after, u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.BwUpMbps != 3 || after.BwDownMbps != 5 || after.QuotaBytes != 1<<30 {
		t.Fatalf("limits not applied: %+v", after)
	}
	if after.ExpiresAt == nil || after.ExpiresAt.Year() != 2030 {
		t.Fatalf("expires_days=0 must keep expiry, got %v", after.ExpiresAt)
	}

	raw := func(rec *httptest.ResponseRecorder) string {
		t.Helper()
		b, err := base64.StdEncoding.DecodeString(rec.Body.String())
		if err != nil {
			t.Fatalf("body not base64: %v", err)
		}
		return string(b)
	}

	// The plan-set expiry governs delivery: past expiry -> empty subscription.
	patch := func(expires string) {
		t.Helper()
		if rec = doJSON(t, r, "PUT", "/api/users/"+utoa(u.ID), map[string]any{
			"expires_at": expires,
		}); rec.Code != http.StatusOK {
			t.Fatalf("patch expiry %s: %d %s", expires, rec.Code, rec.Body)
		}
	}
	// The plan-set expiry governs delivery: past expiry -> nodeless subscription
	// (the TOUCH-7 notice comments remain, per the expired-user convention).
	patch("2020-01-01T00:00:00Z")
	if body := raw(getSub(t, r, "/sub/"+tok, "v2rayNG/1.8")); strings.Contains(body, "a.example.com") {
		t.Fatalf("expired user must not get nodes, got %q", body)
	}

	// Renewal restores delivery with the applied limits untouched.
	patch("2031-01-01T00:00:00Z")
	if body := raw(getSub(t, r, "/sub/"+tok, "v2rayNG/1.8")); !strings.Contains(body, "a.example.com") {
		t.Fatalf("renewed user missing node: %q", body)
	}
	var renewed storage.User
	if err := db.First(&renewed, u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if renewed.BwUpMbps != 3 || renewed.BwDownMbps != 5 {
		t.Fatalf("applied limits lost after renewal: %+v", renewed)
	}
}

// TestPlanBandwidthRules pins the plan bandwidth rules (F-1205): the plan's
// caps are apply-time templates -- editing the plan never propagates to
// already-applied users, re-apply does; a plan without bandwidth fields
// applies 0 (= unlimited, the user-surface convention); a negative cap fails
// validation on update as on create.
func TestPlanBandwidthRules(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "bw-u"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	uid := uint(mustJSON(t, rec)["id"].(float64))

	// A plan with explicit caps; apply lands them on the user row.
	rec = doJSON(t, r, "POST", "/api/plans", map[string]any{
		"name": "bw-pkg", "bw_up_mbps": 3, "bw_down_mbps": 5,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plan: %d %s", rec.Code, rec.Body)
	}
	plan := uint(mustJSON(t, rec)["id"].(float64))
	if rec = doJSON(t, r, "POST", "/api/plans/"+utoa(plan)+"/apply", map[string]any{
		"user_ids": []any{uid},
	}); rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}

	// Editing the plan does NOT move the applied user (no retro propagation).
	if rec = doJSON(t, r, "PUT", "/api/plans/"+utoa(plan), map[string]any{
		"bw_up_mbps": 10, "bw_down_mbps": 10,
	}); rec.Code != http.StatusOK {
		t.Fatalf("update plan: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	if err := db.First(&u, uid).Error; err != nil {
		t.Fatal(err)
	}
	if u.BwUpMbps != 3 || u.BwDownMbps != 5 {
		t.Fatalf("plan edit leaked to applied user: %+v", u)
	}

	// Re-apply is the propagation mechanism.
	if rec = doJSON(t, r, "POST", "/api/plans/"+utoa(plan)+"/apply", map[string]any{
		"user_ids": []any{uid},
	}); rec.Code != http.StatusOK {
		t.Fatalf("re-apply: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&u, uid).Error; err != nil {
		t.Fatal(err)
	}
	if u.BwUpMbps != 10 || u.BwDownMbps != 10 {
		t.Fatalf("re-apply did not propagate caps: %+v", u)
	}

	// A plan without bandwidth fields applies 0 = unlimited.
	rec = doJSON(t, r, "POST", "/api/plans", map[string]any{"name": "bw-none"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bare plan: %d %s", rec.Code, rec.Body)
	}
	bare := uint(mustJSON(t, rec)["id"].(float64))
	if rec = doJSON(t, r, "POST", "/api/plans/"+utoa(bare)+"/apply", map[string]any{
		"user_ids": []any{uid},
	}); rec.Code != http.StatusOK {
		t.Fatalf("bare apply: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&u, uid).Error; err != nil {
		t.Fatal(err)
	}
	if u.BwUpMbps != 0 || u.BwDownMbps != 0 {
		t.Fatalf("bare plan must apply unlimited (0): %+v", u)
	}

	// Negative caps fail on update too (merged-row validation).
	if rec = doJSON(t, r, "PUT", "/api/plans/"+utoa(plan), map[string]any{
		"bw_up_mbps": -1,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative bw update = %d", rec.Code)
	}
}
