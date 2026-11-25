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

// loginAs sends one admin login request (the login endpoint has no auth).
func loginAs(t *testing.T, r *gin.Engine, body map[string]any) (int, map[string]any) {
	t.Helper()
	rec := doPanel(t, r, "", "POST", "/admin/login", body)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// TestLoginAuditErrors covers the login audit error branches: audit persist
// failure, consecutive-failure count failure, alert dedup count failure, and
// alert event persist failure -- the login response is unaffected.
func TestLoginAuditErrors(t *testing.T) {
	// audit persist failure -> 401 as usual, no audit row.
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

	// consecutive-failure count failure -> skipped silently, 401 as usual.
	r2, db2 := newTestRouterWithDB(t)
	failNthCount("t:llcount", db2, 1)
	if code, _ := loginAs(t, r2, map[string]any{"username": "ghost", "password": "x"}); code != http.StatusUnauthorized {
		t.Fatalf("count fail: %d", code)
	}

	// the fifth failure triggers the alert: dedup count failure -> silently emit no event.
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

	// the fifth failure triggers the alert: event persist failure -> silent.
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

// TestLoginLogsAPIErrors covers login audit paging parameter clamping and
// 500 on fetch failure.
func TestLoginLogsAPIErrors(t *testing.T) {
	// out-of-range page/page_size fall back to the defaults.
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
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

	// total count failure -> 500.
	r2, db2 := newTestRouterWithDB(t)
	seedAdminForJWT(t, db2)
	failNthCount("t:lltotal", db2, 1)
	if rec := doAdmin(t, r2, "GET", "/admin/login-logs", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("total: %d %s", rec.Code, rec.Body)
	}

	// row fetch failure -> 500 (the count passes first).
	r3, db3 := newTestRouterWithDB(t)
	seedAdminForJWT(t, db3)
	regQueryFail("t:llrows", db3, (*[]storage.LoginLog)(nil))
	if rec := doAdmin(t, r3, "GET", "/admin/login-logs", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("rows: %d %s", rec.Code, rec.Body)
	}
}
