package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// failNthSetting 让第 n 次 settings 读取（First 目标 *storage.Setting）失败，
// 区分同一请求内多个键的读取顺序。
func failNthSetting(name string, db *gorm.DB, n int) {
	calls := 0
	db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.Setting); ok {
			calls++
			if calls == n {
				tx.AddError(errors.New("db down"))
			}
		}
	})
}

// failNthSettingSave 让第 n 次 settings 保存（Save 走 Update 链，Model
// *storage.Setting）失败，区分 url 与 secret 两步落库。
func failNthSettingSave(name string, db *gorm.DB, n int) {
	calls := 0
	db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Setting); ok {
			calls++
			if calls == n {
				tx.AddError(errors.New("db down"))
			}
		}
	})
}

// TestNotifyErrors 覆盖通知渠道管理错误面：两处键读取失败 500、坏载荷 400、
// url 与 secret 两步保存各自失败 500、测试外发对端报错 502。
func TestNotifyErrors(t *testing.T) {
	// url 键读取失败 → 500。
	r, db := newTestRouterWithDB(t)
	failNthSetting("t:nfyurl", db, 1)
	if rec := doAdmin(t, r, "GET", "/admin/notify", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("url read: %d %s", rec.Code, rec.Body)
	}

	// secret 键读取失败 → 500（url 读取先放行）。
	r2, db2 := newTestRouterWithDB(t)
	failNthSetting("t:nfysecret", db2, 2)
	if rec := doAdmin(t, r2, "GET", "/admin/notify", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("secret read: %d %s", rec.Code, rec.Body)
	}

	// 坏载荷 → 400。
	if rec := doAdmin(t, r2, "PUT", "/admin/notify", []byte("{bad")); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}

	// url 保存失败 → 500。
	r3, db3 := newTestRouterWithDB(t)
	failNthSettingSave("t:nfyurls", db3, 1)
	if rec := doAdmin(t, r3, "PUT", "/admin/notify", []byte(`{"url":"http://x.c"}`)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("url save: %d %s", rec.Code, rec.Body)
	}

	// secret 保存失败 → 500（url 保存先放行）。
	r4, db4 := newTestRouterWithDB(t)
	failNthSettingSave("t:nfysecrets", db4, 2)
	if rec := doAdmin(t, r4, "PUT", "/admin/notify", []byte(`{"url":"http://x.c","secret":"k"}`)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("secret save: %d %s", rec.Code, rec.Body)
	}

	// 测试外发：对端回 5xx → 502。
	r5 := newTestRouter(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if rec := doAdmin(t, r5, "PUT", "/admin/notify", []byte(`{"url":"`+srv.URL+`"}`)); rec.Code != http.StatusOK {
		t.Fatalf("put notify: %d %s", rec.Code, rec.Body)
	}
	if rec := doAdmin(t, r5, "POST", "/admin/notify/test", nil); rec.Code != http.StatusBadGateway {
		t.Fatalf("test notify: %d %s", rec.Code, rec.Body)
	}
}
