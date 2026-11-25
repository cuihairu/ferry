package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// botFixture builds a router with the bot token enabled and binds a tg chat_id
// to bot-user (TOUCH-1 scope), returning the router, the DB, and the user id.
func botFixture(t *testing.T, chatID string) (*gin.Engine, *gorm.DB, uint) {
	t.Helper()
	r, db := newBotRouter(t)
	panelToken(t, r, "bot-user")
	var uid uint
	if err := db.Model(&storage.User{}).Where("username = ?", "bot-user").Pluck("id", &uid).Error; err != nil || uid == 0 {
		t.Fatalf("find user: %v %d", err, uid)
	}
	if err := db.Create(&storage.UserContact{
		UserID: uid, Email: "b@x.c", TgChatID: chatID, RoutineEmails: true, BoundAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	return r, db, uid
}

// botGet calls /api/bot/summary: the Bearer header and the X-Ferry-Bot-Token
// header are passed separately so the fallback path and the missing case can be
// tested independently. An empty chatID means no query string is set.
func botGet(t *testing.T, r *gin.Engine, chatID, bearer, header string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/bot/summary", nil)
	if chatID != "" {
		req.URL.RawQuery = "chat_id=" + chatID
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if header != "" {
		req.Header.Set("X-Ferry-Bot-Token", header)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestBotSummaryHeaderFallback covers the X-Ferry-Bot-Token fallback when no
// Bearer header is present: a token hit resolves the user (200), equivalent to Bearer.
func TestBotSummaryHeaderFallback(t *testing.T) {
	r, _, _ := botFixture(t, "888002")
	if rec := botGet(t, r, "888002", "", "bot-secret"); rec.Code != http.StatusOK {
		t.Fatalf("header fallback: %d %s", rec.Code, rec.Body)
	}
}

// TestBotSummaryMissingChatID covers a service-level token hit with a missing chat_id -> 400.
func TestBotSummaryMissingChatID(t *testing.T) {
	r, _, _ := botFixture(t, "888003")
	if rec := botGet(t, r, "", "bot-secret", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing chat_id: %d %s", rec.Code, rec.Body)
	}
}

// TestBotSummaryUsageScanFail covers a failed usage summary Scan -> 500 (error
// injected on the Row chain; usageQuery targets the traffic table via Model).
func TestBotSummaryUsageScanFail(t *testing.T) {
	r, db, _ := botFixture(t, "888004")
	db.Callback().Row().Before("gorm:row").Register("t:bot_row", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.TrafficLog); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := botGet(t, r, "888004", "bot-secret", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("usage scan fail: %d %s", rec.Code, rec.Body)
	}
}

// TestBotSummaryOrdersFail covers a failed recent-orders query -> 500 (the usage summary already returned fine).
func TestBotSummaryOrdersFail(t *testing.T) {
	r, db, _ := botFixture(t, "888005")
	regQueryFail("t:bot_orders", db, (*[]storage.PaymentOrder)(nil))
	if rec := botGet(t, r, "888005", "bot-secret", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("orders fail: %d %s", rec.Code, rec.Body)
	}
}
