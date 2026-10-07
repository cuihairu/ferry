package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newBotRouter 定制配置路由器：BotToken 开启的 TG bot 对接面（TOUCH-6）。
func newBotRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	gin.SetMode(gin.TestMode)
	cfg := config.Default()
	cfg.BotToken = "bot-secret"
	r, _ := NewRouter(db, cfg)
	return r, db
}

// TestBotSummary 覆盖 TG bot 对接面（TOUCH-6）：令牌未配置 404（防探测）、
// 令牌错误 401、chat_id 未绑 404、命中返回用量/状态/最近订单/购买入口、
// 禁用用户 404。
func TestBotSummary(t *testing.T) {
	// 未配置令牌的默认路由器：统一 404 防探测
	r0, db0 := newTestRouterWithDB(t)
	panelToken(t, r0, "bot-user0")
	if rec := doJSON(t, r0, "GET", "/api/bot/summary?chat_id=x", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no-token config: %d %s", rec.Code, rec.Body)
	}
	_ = db0

	r, db := newBotRouter(t)
	panelToken(t, r, "bot-user")
	var uid uint
	if err := db.Model(&storage.User{}).Where("username = ?", "bot-user").Pluck("id", &uid).Error; err != nil || uid == 0 {
		t.Fatalf("find user: %v %d", err, uid)
	}
	if err := db.Create(&storage.UserContact{
		UserID: uid, Email: "b@x.c", TgChatID: "888001", RoutineEmails: true, BoundAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: "BO-1", UserID: uid, Provider: "card", Product: "月卡",
		AmountCents: 1000, Status: "paid", CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.TrafficLog{
		UserID: uid, RxBytes: 1000, TxBytes: 500, RecordedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	get := func(chatID, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/bot/summary?chat_id="+chatID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// 令牌错误 401
	if rec := get("888001", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	// 未绑定 chat_id 404
	if rec := get("999", "bot-secret"); rec.Code != http.StatusNotFound {
		t.Fatalf("unbound chat: %d", rec.Code)
	}
	// 命中：用量/状态/订单/购买入口
	rec := get("888001", "bot-secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("summary: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Username   string `json:"username"`
		UsedBytes  int64  `json:"used_bytes"`
		QuotaBytes int64  `json:"quota_bytes"`
		Active     bool   `json:"active"`
		Stale      bool   `json:"stale"`
		BuyURL     string `json:"buy_url"`
		Orders     []struct {
			OrderNo string `json:"order_no"`
			Status  string `json:"status"`
		} `json:"orders"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Username != "bot-user" || out.UsedBytes != 1500 || !out.Active || out.Stale {
		t.Fatalf("unexpected summary: %+v", out)
	}
	if len(out.Orders) != 1 || out.Orders[0].OrderNo != "BO-1" || out.Orders[0].Status != "paid" {
		t.Fatalf("unexpected orders: %+v", out.Orders)
	}
	if out.BuyURL != "http://localhost:8080/panel" {
		t.Fatalf("buy url: %s", out.BuyURL)
	}

	// 禁用用户 404
	if err := db.Model(&storage.User{}).Where("username = ?", "bot-user").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if rec := get("888001", "bot-secret"); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled user: %d", rec.Code)
	}
}
