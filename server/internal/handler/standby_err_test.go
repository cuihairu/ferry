package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
	"gorm.io/gorm"
)

// namedDialector overrides only the dialect name (to hit standby's sqlite-only
// branch); the other capabilities fall back to the original dialect.
type namedDialector struct {
	gorm.Dialector
	name string
}

func (n namedDialector) Name() string { return n.name }

// standbyGet calls the snapshot endpoint on a router configured with a standby token.
func standbyGet(t *testing.T, r http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/standby/snapshot", nil)
	if token != "" {
		req.Header.Set("X-Standby-Token", token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestStandbySnapshotNotSQLite covers a non-sqlite dialect -> 500 (snapshots only support sqlite).
func TestStandbySnapshotNotSQLite(t *testing.T) {
	r, db := newTestRouterCfg(t, func(c *config.Config) { c.StandbyToken = "st" })
	db.Dialector = namedDialector{Dialector: db.Dialector, name: "mysql"}
	if rec := standbyGet(t, r, "st"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("non-sqlite: %d %s", rec.Code, rec.Body)
	}
}

// TestStandbySnapshotSnapshotFail covers snapshot production failure -> 500: the db
// handle is closed, and the VACUUM INTO error is passed through as 500.
func TestStandbySnapshotSnapshotFail(t *testing.T) {
	r, db := newTestRouterCfg(t, func(c *config.Config) { c.StandbyToken = "st" })
	closeSQL(t, db)
	if rec := standbyGet(t, r, "st"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("snapshot fail: %d %s", rec.Code, rec.Body)
	}
}
