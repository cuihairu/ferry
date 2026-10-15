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

// botFixture 建 bot 令牌开启的路由并给 bot-user 绑一个 tg chat_id（TOUCH-1
// 口径），返回路由、库与用户 id。
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

// botGet 调 /api/bot/summary：Bearer 头与 X-Ferry-Bot-Token 头分开传，便于
// 分别测兜底路径与缺失。chatID 为空即不带上 query。
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

// TestBotSummaryHeaderFallback 覆盖无 Bearer 头时 X-Ferry-Bot-Token 兜底：
// 令牌命中即可命中用户（200），与 Bearer 等价。
func TestBotSummaryHeaderFallback(t *testing.T) {
	r, _, _ := botFixture(t, "888002")
	if rec := botGet(t, r, "888002", "", "bot-secret"); rec.Code != http.StatusOK {
		t.Fatalf("header fallback: %d %s", rec.Code, rec.Body)
	}
}

// TestBotSummaryMissingChatID 覆盖服务级令牌命中但缺 chat_id → 400。
func TestBotSummaryMissingChatID(t *testing.T) {
	r, _, _ := botFixture(t, "888003")
	if rec := botGet(t, r, "", "bot-secret", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing chat_id: %d %s", rec.Code, rec.Body)
	}
}

// TestBotSummaryUsageScanFail 覆盖用量汇总 Scan 失败 → 500（Row 链注错，
// usageQuery 走 Model 定位流量表）。
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

// TestBotSummaryOrdersFail 覆盖最近订单查询失败 → 500（用量汇总已正常返回）。
func TestBotSummaryOrdersFail(t *testing.T) {
	r, db, _ := botFixture(t, "888005")
	regQueryFail("t:bot_orders", db, (*[]storage.PaymentOrder)(nil))
	if rec := botGet(t, r, "888005", "bot-secret", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("orders fail: %d %s", rec.Code, rec.Body)
	}
}
