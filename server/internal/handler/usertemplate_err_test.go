package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestUserTemplateErrors 覆盖默认模板错误面：读取库故障、坏载荷、
// 缺 reset_cycle 回落缺省周期、保存库故障。
func TestUserTemplateErrors(t *testing.T) {
	// 读取库故障 → 500。
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:tplsetting", db, (*storage.Setting)(nil))
	if rec := doJSON(t, r, "GET", "/api/user-template", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("get template: %d %s", rec.Code, rec.Body)
	}

	// 坏载荷 → 400。
	if rec := doRaw(t, r, "PUT", "/api/user-template", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}

	// 缺 reset_cycle 落缺省周期（不回填用户输入原样）。
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

	// 保存库故障 → 500（SetSetting 走 Save 的 Update 链）。
	db.Callback().Update().Before("gorm:update").Register("t:tplsave", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Setting); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, "PUT", "/api/user-template", map[string]any{"quota_bytes": 2048}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save template: %d %s", rec.Code, rec.Body)
	}
}

// TestUserTemplateDegradedRows 覆盖存量 settings 脏值回落缺省模板：
// JSON 不可解、reset_cycle 空、带宽脏值三态均按缺省模板兜底不阻断。
func TestUserTemplateDegradedRows(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raw       string
		wantCycle string
		wantQuota int64
	}{
		{"bad json", "{bad", "none", 0},                                  // 整体坏 → 全缺省
		{"empty cycle", `{"quota_bytes":1,"reset_cycle":""}`, "none", 1}, // 只补周期，其余保留
		{"bad bw", `{"quota_bytes":1,"bw_up_mbps":-1}`, "none", 0},       // 带宽脏 → 整体回缺省
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
