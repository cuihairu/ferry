package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestUserTemplateErrors covers the default template error surface: read db
// failure, bad payload, missing reset_cycle falling back to the default cycle,
// and save db failure.
func TestUserTemplateErrors(t *testing.T) {
	// Read db failure -> 500.
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:tplsetting", db, (*storage.Setting)(nil))
	if rec := doJSON(t, r, "GET", "/api/user-template", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("get template: %d %s", rec.Code, rec.Body)
	}

	// Bad payload -> 400.
	if rec := doRaw(t, r, "PUT", "/api/user-template", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}

	// Missing reset_cycle lands the default cycle (no verbatim echo of the user input).
	rec := doJSON(t, r, "PUT", "/api/user-template", map[string]any{"quota_bytes": 1024})
	if rec.Code != http.StatusOK {
		t.Fatalf("put template: %d %s", rec.Code, rec.Body)
	}
	var in struct {
		ResetCycle string `json:"reset_cycle"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &in)
	if in.ResetCycle != "none" {
		t.Fatalf("default cycle = %q", in.ResetCycle)
	}

	// Save db failure -> 500 (SetSetting goes through Save's Update chain).
	db.Callback().Update().Before("gorm:update").Register("t:tplsave", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Setting); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, "PUT", "/api/user-template", map[string]any{"quota_bytes": 2048}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save template: %d %s", rec.Code, rec.Body)
	}
}

// TestUserTemplateDegradedRows covers stored settings dirty values degrading to
// the default template: unparseable JSON, empty reset_cycle and dirty bandwidth
// values all fall back to the default template without blocking.
func TestUserTemplateDegradedRows(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raw       string
		wantCycle string
		wantQuota int64
	}{
		{"bad json", "{bad", "none", 0},                                  // whole payload bad -> all defaults
		{"empty cycle", `{"quota_bytes":1,"reset_cycle":""}`, "none", 1}, // only the cycle is filled in, the rest kept
		{"bad bw", `{"quota_bytes":1,"bw_up_mbps":-1}`, "none", 0},       // dirty bandwidth -> whole payload back to defaults
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db := newTestRouterWithDB(t)
			if err := storage.SetSetting(db, "user_template", tc.raw); err != nil {
				t.Fatal(err)
			}
			rec := doJSON(t, r, "GET", "/api/user-template", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("get template: %d %s", rec.Code, rec.Body)
			}
			var tpl struct {
				QuotaBytes int64  `json:"quota_bytes"`
				ResetCycle string `json:"reset_cycle"`
				BwUpMbps   int    `json:"bw_up_mbps"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &tpl); err != nil {
				t.Fatal(err)
			}
			if tpl.ResetCycle != tc.wantCycle || tpl.QuotaBytes != tc.wantQuota || tpl.BwUpMbps != 0 {
				t.Fatalf("degraded template = %+v", tpl)
			}
		})
	}
}
