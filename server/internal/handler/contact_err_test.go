package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// doPanelRaw follows the same convention as doPanel but writes the payload verbatim (for bad-JSON negative cases).
func doPanelRaw(t *testing.T, r *gin.Engine, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestPanelContactErrors covers the contact binding error surface: no
// credentials, bad payload, TG chat too long, first-bind persist failure, and
// rebind save failure.
func TestPanelContactErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	token := panelToken(t, r, "ct-err")

	// PUT without credentials -> 404 (the panelUser no-hit branch).
	if rec := doPanel(t, r, "", "PUT", "/api/panel/contact", map[string]any{"email": "a@b.c"}); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
	// Bad payload -> 400.
	if rec := doPanelRaw(t, r, token, "PUT", "/api/panel/contact", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
	// TG chat_id too long -> 400.
	if rec := doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{
		"tg_chat_id": strings.Repeat("x", 65),
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("chat too long: %d", rec.Code)
	}

	// First-bind persist failure -> 500 (Create chain).
	r2, db2 := newTestRouterWithDB(t)
	token2 := panelToken(t, r2, "ct-new")
	db2.Callback().Create().Before("gorm:create").Register("t:ctcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.UserContact); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doPanel(t, r2, token2, "PUT", "/api/panel/contact", map[string]any{"email": "a@b.c"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}

	// Rebind save failure -> 500 (Save goes through the Update chain).
	r3, db3 := newTestRouterWithDB(t)
	token3 := panelToken(t, r3, "ct-save")
	if rec := doPanel(t, r3, token3, "PUT", "/api/panel/contact", map[string]any{"email": "a@b.c"}); rec.Code != http.StatusOK {
		t.Fatalf("first bind: %d %s", rec.Code, rec.Body)
	}
	db3.Callback().Update().Before("gorm:update").Register("t:ctsave", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.UserContact); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doPanel(t, r3, token3, "PUT", "/api/panel/contact", map[string]any{"email": "b@b.c"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save fail: %d %s", rec.Code, rec.Body)
	}
}

// TestTouchResultContactErrors covers the receipt-linkage fallback creation of
// the contact row and the silence when that creation fails (the receipt
// endpoint must not error on a linkage failure).
func TestTouchResultContactErrors(t *testing.T) {
	// User has no contact row -> a failed receipt auto-creates a zero-value row and marks it stale.
	r, db := newSecretRouter(t)
	var uid uint
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "ct-norow"}); rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	if err := db.Model(&storage.User{}).Where("username = ?", "ct-norow").Pluck("id", &uid).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.TouchJob{
		UserID: int64(uid), Channel: "email", Kind: "bill",
		Payload: `{}`, Status: "sent", EventID: 6001, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.Event{ID: 6001, Kind: "bill", Target: herald.TargetUser(int64(uid)),
		Status: "sent", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doSignedJSON(t, r, "/api/internal/event-results", map[string]any{
		"event_id": 6001, "channel": "email", "status": "failed", "detail": "x",
	}); rec.Code != http.StatusOK {
		t.Fatalf("result: %d %s", rec.Code, rec.Body)
	}
	var ct storage.UserContact
	if err := db.First(&ct, uid).Error; err != nil {
		t.Fatalf("fallback row missing: %v", err)
	}
	if !ct.Stale || ct.FailStreak != 1 {
		t.Fatalf("fallback row = %+v", ct)
	}

	// Fallback creation fails -> return silently, the receipt endpoint still returns 200.
	r2, db2 := newSecretRouter(t)
	var uid2 uint
	if rec := doJSON(t, r2, "POST", "/api/users", map[string]any{"username": "ct-norow2"}); rec.Code != http.StatusCreated {
		t.Fatalf("create user2: %d %s", rec.Code, rec.Body)
	}
	if err := db2.Model(&storage.User{}).Where("username = ?", "ct-norow2").Pluck("id", &uid2).Error; err != nil {
		t.Fatal(err)
	}
	if err := db2.Create(&storage.TouchJob{
		UserID: int64(uid2), Channel: "email", Kind: "bill",
		Payload: `{}`, Status: "sent", EventID: 6002, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db2.Create(&storage.Event{ID: 6002, Kind: "bill", Target: herald.TargetUser(int64(uid2)),
		Status: "sent", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	db2.Callback().Create().Before("gorm:create").Register("t:ctfallback", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.UserContact); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doSignedJSON(t, r2, "/api/internal/event-results", map[string]any{
		"event_id": 6002, "channel": "email", "status": "failed", "detail": "x",
	}); rec.Code != http.StatusOK {
		t.Fatalf("result with fallback fail: %d %s", rec.Code, rec.Body)
	}
	var n int64
	db2.Model(&storage.UserContact{}).Count(&n)
	if n != 0 {
		t.Fatalf("fallback should not write: %d", n)
	}
}

// TestTruncateErr covers truncation of an over-long receipt detail (column
// width 255); after the P2 UTF-8 truncation fix, truncation backs off to a
// character boundary — a Chinese-bearing detail stays valid UTF-8 after
// truncation.
func TestTruncateErr(t *testing.T) {
	long := strings.Repeat("x", 300)
	if got := truncateErr(long); len(got) != 255 {
		t.Fatalf("truncate = %d, want 255", len(got))
	}
	if got := truncateErr("short"); got != "short" {
		t.Fatalf("passthrough = %q", got)
	}
	// The limit falls in the middle of a multi-byte character (x fills up to 253 and the first byte of the CJK character used below occupies 253-255): back off to the boundary.
	longZH := strings.Repeat("x", 253) + strings.Repeat("流", 10)
	if got := truncateErr(longZH); len(got) != 253 || !utf8.ValidString(got) {
		t.Fatalf("utf8 truncate = %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
}
