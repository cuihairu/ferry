package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// doPanelRaw 与 doPanel 同口径但载荷原样写入（坏 JSON 负例用）。
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

// TestPanelContactErrors 覆盖联系绑定错误面：无凭据、坏载荷、TG chat 过长、
// 首绑落库失败、换绑保存失败。
func TestPanelContactErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	token := panelToken(t, r, "ct-err")

	// 无凭据 PUT → 404（panelUser 未命中分支）。
	if rec := doPanel(t, r, "", "PUT", "/api/panel/contact", map[string]any{"email": "a@b.c"}); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
	// 坏载荷 → 400。
	if rec := doPanelRaw(t, r, token, "PUT", "/api/panel/contact", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
	// TG chat_id 超长 → 400。
	if rec := doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{
		"tg_chat_id": strings.Repeat("x", 65),
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("chat too long: %d", rec.Code)
	}

	// 首绑落库失败 → 500（Create 链）。
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

	// 换绑保存失败 → 500（Save 走 Update 链）。
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

// TestTouchResultContactErrors 覆盖回执联动的联系行兜底建档与建档失败静默
// （回执端点不因联动失败报错）。
func TestTouchResultContactErrors(t *testing.T) {
	// 用户无联系行 → failed 回执自动建零值行并标 stale。
	r, db := newTestRouterWithDB(t)
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
	if rec := doJSON(t, r, "POST", "/api/internal/event-results", map[string]any{
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

	// 兜底建档失败 → 静默返回，回执端点仍 200。
	r2, db2 := newTestRouterWithDB(t)
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
	if rec := doJSON(t, r2, "POST", "/api/internal/event-results", map[string]any{
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

// TestTruncateErr 覆盖回执 detail 超长截断（列宽 255）。
func TestTruncateErr(t *testing.T) {
	long := strings.Repeat("x", 300)
	if got := truncateErr(long); len(got) != 255 {
		t.Fatalf("truncate = %d, want 255", len(got))
	}
	if got := truncateErr("short"); got != "short" {
		t.Fatalf("passthrough = %q", got)
	}
}
