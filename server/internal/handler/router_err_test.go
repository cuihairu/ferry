package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestHealthProbeUnwritable covers a failed self-check write probe ->
// db_writable=false and status degraded, still returning 200 (so liveness
// probes do not kill the instance by mistake).
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

// TestStatusListDBErrors covers read failures on the dimension status lights and
// the probe report list returning 500, plus a valid limit being honored on the
// probe list.
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
