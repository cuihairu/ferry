package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestHealthProbeUnwritable 覆盖自检写探针失败 → db_writable=false 且
// status degraded，仍回 200（探活方不误杀）。
func TestHealthProbeUnwritable(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	db.Callback().Create().Before("gorm:create").Register("t:healthprobe", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.Setting); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	rec := doJSON(t, r, "GET", "/api/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("health: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Status string `json:"status"`
		Checks struct {
			DBWritable bool `json:"db_writable"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Checks.DBWritable {
		t.Fatalf("db_writable should be false on probe failure: %+v", out)
	}
	if out.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", out.Status)
	}
}

// TestStatusListDBErrors 覆盖维度状态灯与探测存证列表取数失败 500，以及
// 探测列表合法 limit 生效。
func TestStatusListDBErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:dimlist", db, (*[]storage.DimensionStatus)(nil))
	if rec := doJSON(t, r, "GET", "/api/dimension-status", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("dimension list: %d %s", rec.Code, rec.Body)
	}

	r2, db2 := newTestRouterWithDB(t)
	regQueryFail("t:probelist", db2, (*[]storage.ProbeReport)(nil))
	if rec := doJSON(t, r2, "GET", "/api/probe-reports", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("probe list: %d %s", rec.Code, rec.Body)
	}

	r3, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r3, "GET", "/api/probe-reports?limit=5", nil); rec.Code != http.StatusOK {
		t.Fatalf("probe limit: %d %s", rec.Code, rec.Body)
	}
}
