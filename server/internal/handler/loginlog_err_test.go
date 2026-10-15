package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// loginAs 发一次管理员登录请求（登录端点无鉴权）。
func loginAs(t *testing.T, r *gin.Engine, body map[string]any) (int, map[string]any) {
	t.Helper()
	rec := doPanel(t, r, "", "POST", "/admin/login", body)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// TestLoginAuditErrors 覆盖登录审计错误分支：审计落库失败、连续失败计数
// 失败、告警去重计数失败、告警事件落库失败——登录响应不受影响。
func TestLoginAuditErrors(t *testing.T) {
	// 审计落库失败 → 401 照常，无审计行。
	r, db := newTestRouterWithDB(t)
	db.Callback().Create().Before("gorm:create").Register("t:llcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.LoginLog); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if code, _ := loginAs(t, r, map[string]any{"username": "ghost", "password": "x"}); code != http.StatusUnauthorized {
		t.Fatalf("write fail: %d", code)
	}
	var n int64
	db.Model(&storage.LoginLog{}).Count(&n)
	if n != 0 {
		t.Fatalf("audit wrote %d rows", n)
	}

	// 连续失败计数失败 → 静默跳过，401 照常。
	r2, db2 := newTestRouterWithDB(t)
	failNthCount("t:llcount", db2, 1)
	if code, _ := loginAs(t, r2, map[string]any{"username": "ghost", "password": "x"}); code != http.StatusUnauthorized {
		t.Fatalf("count fail: %d", code)
	}

	// 第五次失败触发告警：去重计数失败 → 静默不落事件。
	r3, db3 := newTestRouterWithDB(t)
	db3.Callback().Query().Before("gorm:query").Register("t:llevcount", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Event); ok {
			if _, ok := tx.Statement.Dest.(*int64); ok {
				tx.AddError(errors.New("db down"))
			}
		}
	})
	for i := 0; i < 5; i++ {
		if code, _ := loginAs(t, r3, map[string]any{"username": "ghost", "password": "x"}); code != http.StatusUnauthorized {
			t.Fatalf("fail #%d: %d", i+1, code)
		}
	}
	var ev int64
	db3.Model(&storage.Event{}).Count(&ev)
	if ev != 0 {
		t.Fatalf("dedup count fail wrote %d events", ev)
	}

	// 第五次失败触发告警：事件落库失败 → 静默。
	r4, db4 := newTestRouterWithDB(t)
	db4.Callback().Create().Before("gorm:create").Register("t:llevcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.Event); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	for i := 0; i < 5; i++ {
		if code, _ := loginAs(t, r4, map[string]any{"username": "ghost", "password": "x"}); code != http.StatusUnauthorized {
			t.Fatalf("fail #%d: %d", i+1, code)
		}
	}
	var ev4 int64
	db4.Model(&storage.Event{}).Count(&ev4)
	if ev4 != 0 {
		t.Fatalf("emit fail wrote %d events", ev4)
	}
}

// TestLoginLogsAPIErrors 覆盖登录审计分页参数夹取与取数失败 500。
func TestLoginLogsAPIErrors(t *testing.T) {
	// 页码/页大小越界回落缺省。
	r, _ := newTestRouterWithDB(t)
	for _, q := range []string{"page=0", "page_size=0", "page_size=500"} {
		rec := doAdmin(t, r, "GET", "/admin/login-logs?"+q, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
		}
		var out struct {
			Page     int `json:"page"`
			PageSize int `json:"page_size"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.Page != 1 || out.PageSize != 20 {
			t.Fatalf("%s clamp = page %d size %d", q, out.Page, out.PageSize)
		}
	}

	// 总数计数失败 → 500。
	r2, db2 := newTestRouterWithDB(t)
	failNthCount("t:lltotal", db2, 1)
	if rec := doAdmin(t, r2, "GET", "/admin/login-logs", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("total: %d %s", rec.Code, rec.Body)
	}

	// 明细取数失败 → 500（计数先放行）。
	r3, db3 := newTestRouterWithDB(t)
	regQueryFail("t:llrows", db3, (*[]storage.LoginLog)(nil))
	if rec := doAdmin(t, r3, "GET", "/admin/login-logs", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("rows: %d %s", rec.Code, rec.Body)
	}
}
