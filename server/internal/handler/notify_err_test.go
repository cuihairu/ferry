package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// failNthSetting makes the n-th settings read (First targets *storage.Setting)
// fail, distinguishing the read order of multiple keys in one request.
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

// failNthSettingSave makes the n-th settings save (Save runs the Update chain,
// Model *storage.Setting) fail, distinguishing the two-step url/secret writes.
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

// TestNotifyErrors covers the notification channel management error surface:
// both key reads failing 500, bad payload 400, the url and secret save steps
// each failing 500, and the test-send peer erroring 502.
func TestNotifyErrors(t *testing.T) {
	// url key read fails -> 500.
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	failNthSetting("t:nfyurl", db, 1)
	if rec := doAdmin(t, r, "GET", "/admin/notify", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("url read: %d %s", rec.Code, rec.Body)
	}

	// secret key read fails -> 500 (the url read is let through first).
	r2, db2 := newTestRouterWithDB(t)
	seedAdminForJWT(t, db2)
	failNthSetting("t:nfysecret", db2, 2)
	if rec := doAdmin(t, r2, "GET", "/admin/notify", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("secret read: %d %s", rec.Code, rec.Body)
	}

	// Bad payload -> 400.
	if rec := doAdmin(t, r2, "PUT", "/admin/notify", []byte("{bad")); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}

	// url save fails -> 500.
	r3, db3 := newTestRouterWithDB(t)
	seedAdminForJWT(t, db3)
	failNthSettingSave("t:nfyurls", db3, 1)
	if rec := doAdmin(t, r3, "PUT", "/admin/notify", []byte(`{"url":"http://x.c"}`)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("url save: %d %s", rec.Code, rec.Body)
	}

	// secret save fails -> 500 (the url save is let through first).
	r4, db4 := newTestRouterWithDB(t)
	seedAdminForJWT(t, db4)
	failNthSettingSave("t:nfysecrets", db4, 2)
	if rec := doAdmin(t, r4, "PUT", "/admin/notify", []byte(`{"url":"http://x.c","secret":"k"}`)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("secret save: %d %s", rec.Code, rec.Body)
	}

	// Test-send: the peer returns 5xx -> 502.
	r5, db5 := newTestRouterWithDB(t)
	seedAdminForJWT(t, db5)
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
