package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
	"gorm.io/gorm"
)

// namedDialector 只覆盖方言名（命中 standby 的 sqlite 限定分支），其余能力
// 沿用原方言实现。
type namedDialector struct {
	gorm.Dialector
	name string
}

func (n namedDialector) Name() string { return n.name }

// standbyGet 用配置了 standby 令牌的路由调快照端点。
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

// TestStandbySnapshotNotSQLite 覆盖非 sqlite 方言 → 500（快照只支持 sqlite）。
func TestStandbySnapshotNotSQLite(t *testing.T) {
	r, db := newTestRouterCfg(t, func(c *config.Config) { c.StandbyToken = "st" })
	db.Dialector = namedDialector{Dialector: db.Dialector, name: "mysql"}
	if rec := standbyGet(t, r, "st"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("non-sqlite: %d %s", rec.Code, rec.Body)
	}
}

// TestStandbySnapshotSnapshotFail 覆盖产档失败 → 500：库句柄已关，
// VACUUM INTO 报错由端点原样转 500。
func TestStandbySnapshotSnapshotFail(t *testing.T) {
	r, db := newTestRouterCfg(t, func(c *config.Config) { c.StandbyToken = "st" })
	closeSQL(t, db)
	if rec := standbyGet(t, r, "st"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("snapshot fail: %d %s", rec.Code, rec.Body)
	}
}
