package handler

import (
	"encoding/json"
	"net/http"
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
