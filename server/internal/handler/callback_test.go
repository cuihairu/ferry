package handler

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Herald 事件回调回执（HERALD-5）：回执端点原生解析 §13.5 形状
// （{event_id: "cb-…", app, kind, at, delivery: {…}}），验 X-Herald-Signature
// HMAC；HERALD-2 旧形状（{event_id: int64, …}）双形状判别继续收。

// callbackTestSecret 是测试用 Herald 回调验签密钥（32 字节口径，同
// callback_test.go 既有取值）。
const callbackTestSecret = "callback-secret-0123456789abcdef"

// newSecretRouter 建带 Herald 回调验签密钥的路由器：P2 回执端点默认必验
// 签，凡 POST /api/internal/event-results 的用例都须经此（或
// newCallbackRouter）建带密钥实例。
func newSecretRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	return newTestRouterCfg(t, func(c *config.Config) {
		c.HeraldCallbackSecret = callbackTestSecret
	})
}

// newCallbackRouter 定制配置路由器：配置回调验签密钥的实例。
func newCallbackRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	return newSecretRouter(t)
}

// heraldSign 按 herald 侧口径签名：sha256=<hex HMAC-SHA256(secret, body)>。
func heraldSign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// postEventResult 带可选签名 POST 回执端点。
func postEventResult(t *testing.T, r *gin.Engine, secret string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/internal/event-results", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-Herald-Signature", heraldSign(secret, body))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// doSignedJSON 发带 X-Herald-Signature 的 JSON POST：回执端点默认必验签
// （P2），凡走 /api/internal/event-results 成功路径的用例须带测试密钥签名。
func doSignedJSON(t *testing.T, r *gin.Engine, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Herald-Signature", heraldSign(callbackTestSecret, buf.Bytes()))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// doSignedRawBody 发带签名的原始字节体 POST（坏 JSON 等不能被编码器洗成
// 合法形态的用例）；体读失败（errReader）走 doRawBody 即可，读体先于验签。
func doSignedRawBody(t *testing.T, r *gin.Engine, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Herald-Signature", heraldSign(callbackTestSecret, body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// §13.5 回调载荷样板：delivery.event_id 回带 ferry outbox id（字符串数字）。
func callbackPayload(outboxID string, status, errMsg string) []byte {
	payload := map[string]any{
		"event_id": "cb-8f2c9d41", "app": "ferry", "kind": "delivery_result",
		"at": "2026-10-08T00:00:00Z",
		"delivery": map[string]any{
			"task_id": "t-1", "event_id": outboxID, "audience_id": "user.7",
			"category": "notice", "channel": "webhook", "status": status, "error": errMsg,
		},
	}
	b, _ := json.Marshal(payload)
	return b
}

// TestEventResultHeraldCallback 覆盖 §13.5 原生解析（HERALD-5）：验签
// 通过/失败、success/failed 映射 sent/failed（原因落 detail）、旧形状
// 双形状判别继续收、非数字 event_id、缺字段、unsubscribe ack、
// 未配置密钥不验签（既有通道不断）。
func TestEventResultHeraldCallback(t *testing.T) {
	r, db := newCallbackRouter(t)
	if err := db.Create(&storage.Event{ID: 3001, Kind: "notice", Severity: "info", Title: "t", Target: "user:7", Status: "sent", OccurredAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	// ① 验签通过：§13.5 success → EventDelivery sent
	rec := postEventResult(t, r, "callback-secret-0123456789abcdef", callbackPayload("3001", "success", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("signed callback: %d %s", rec.Code, rec.Body)
	}
	var rows []storage.EventDelivery
	if err := db.Where("event_id = ?", 3001).Find(&rows).Error; err != nil || len(rows) != 1 {
		t.Fatalf("delivery rows: %v %+v", err, rows)
	}
	if rows[0].Status != "sent" || rows[0].Channel != "webhook" {
		t.Fatalf("delivery row: %+v", rows[0])
	}

	// ② 验签失败：篡改签名 401
	bad := postEventResult(t, r, "wrong-secret", callbackPayload("3001", "success", ""))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", bad.Code)
	}
	// 缺签名头（配置了密钥必须带）同样 401
	bare := httptest.NewRequest("POST", "/api/internal/event-results", bytes.NewReader(callbackPayload("3001", "success", "")))
	bare.Header.Set("Content-Type", "application/json")
	bareRec := httptest.NewRecorder()
	r.ServeHTTP(bareRec, bare)
	if bareRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing signature: %d", bareRec.Code)
	}

	// ③ failed → detail 落失败原因；触达任务联动（stale）
	if err := db.Create(&storage.Event{ID: 3002, Kind: "bill", Severity: "info", Title: "t2", Target: "user:7", Status: "sent", OccurredAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.UserContact{UserID: 7, Email: "c@x.c", RoutineEmails: true, BoundAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.TouchJob{UserID: 7, Channel: "email", Kind: "bill", Payload: "{}", Status: "sent", EventID: 3002, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", callbackPayload("3002", "failed", "smtp: relay denied"))
	if rec.Code != http.StatusOK {
		t.Fatalf("failed callback: %d %s", rec.Code, rec.Body)
	}
	var row storage.EventDelivery
	if err := db.Where("event_id = ?", 3002).First(&row).Error; err != nil || row.Status != "failed" || row.Detail != "smtp: relay denied" {
		t.Fatalf("failed row: %v %+v", err, row)
	}
	var ct storage.UserContact
	if err := db.First(&ct, 7).Error; err != nil || !ct.Stale || ct.FailStreak != 1 {
		t.Fatalf("touch linkage: %v %+v", err, ct)
	}

	// ④ 旧形状（HERALD-2）：带正确签名继续收——双形状判别不断流
	old := []byte(`{"event_id":3001,"channel":"herald","status":"sent","detail":"via shim"}`)
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", old)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy shape: %d %s", rec.Code, rec.Body)
	}
	var n int64
	db.Model(&storage.EventDelivery{}).Where("event_id = ? AND channel = ?", 3001, "herald").Count(&n)
	if n != 1 {
		t.Fatalf("legacy row missing: %d", n)
	}

	// ⑤ 非数字 delivery.event_id 400
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", callbackPayload("cb-8f2c", "success", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric event id: %d %s", rec.Code, rec.Body)
	}

	// ⑥ 缺字段：delivery.status 非法 / 缺 channel 各 400
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", callbackPayload("3001", "pending", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status: %d", rec.Code)
	}
	noChan := []byte(`{"event_id":"cb-x","app":"ferry","kind":"delivery_result","at":"2026-10-08T00:00:00Z","delivery":{"event_id":"3001","status":"success"}}`)
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", noChan)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing channel: %d", rec.Code)
	}

	// ⑦ unsubscribe（无 delivery）：ack 忽略防 herald 重试刷
	unsub := []byte(`{"event_id":"cb-1","app":"ferry","kind":"unsubscribe","at":"2026-10-08T00:00:00Z","unsubscribe":{"audience_id":"user.7","category":"notice","channel":"email","relation_type":"surface"}}`)
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", unsub)
	if rec.Code != http.StatusOK {
		t.Fatalf("unsubscribe ack: %d %s", rec.Code, rec.Body)
	}

	// ⑧ 未配置密钥的实例（P2 默认必验签）：两种形状均拒绝——无密钥即无
	// 法确认真伪，伪造回执不被默认接收；且不留任何回执行。
	r0, db0 := newTestRouterWithDB(t)
	if err := db0.Create(&storage.Event{ID: 3001, Kind: "notice", Severity: "info", Title: "t", Target: "user:7", Status: "sent", OccurredAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if rec := postEventResult(t, r0, "", callbackPayload("3001", "success", "")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned callback on secretless instance: %d %s", rec.Code, rec.Body)
	}
	if rec := postEventResult(t, r0, "", []byte(`{"event_id":3001,"channel":"herald","status":"sent"}`)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("legacy on secretless instance: %d %s", rec.Code, rec.Body)
	}
	var n0 int64
	if err := db0.Model(&storage.EventDelivery{}).Where("event_id = ?", 3001).Count(&n0).Error; err != nil || n0 != 0 {
		t.Fatalf("secretless instance recorded delivery: %d err=%v", n0, err)
	}

	// ⑨ 不存在的事件 404（两种形状同口径）
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", callbackPayload("9999", "success", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing event: %d", rec.Code)
	}
}

// TestEventResultSecretlessRejected 覆盖 P2「event-results 默认不验签」修复：
// 回执端点是 open 组公开路由，未配置 HeraldCallbackSecret 的实例不能接受任
// 何回调（默认必验签）——修复前空密钥直接放行，伪造回执（含触达联动副作用）
// 被默认接收；修复后无密钥实例两种形状统一 401 且不留回执行。
func TestEventResultSecretlessRejected(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if err := db.Create(&storage.Event{ID: 4001, Kind: "bill", Severity: "info", Title: "t", Target: "user:9", Status: "sent", OccurredAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.UserContact{UserID: 9, Email: "f@x.c", RoutineEmails: true, BoundAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.TouchJob{UserID: 9, Channel: "email", Kind: "bill", Payload: "{}", Status: "sent", EventID: 4001, CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	// §13.5 回调与旧形状：无密钥实例一律 401，触达联动副作用不触发。
	for _, body := range [][]byte{
		callbackPayload("4001", "failed", "smtp: forged"),
		[]byte(`{"event_id":4001,"channel":"email","status":"failed","detail":"forged"}`),
	} {
		if rec := postEventResult(t, r, "", body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("secretless receipt should 401: %d %s", rec.Code, rec.Body)
		}
	}
	var n int64
	db.Model(&storage.EventDelivery{}).Where("event_id = ?", 4001).Count(&n)
	if n != 0 {
		t.Fatalf("secretless instance recorded delivery: %d", n)
	}
	var ct storage.UserContact
	if err := db.First(&ct, 9).Error; err != nil || ct.Stale {
		t.Fatalf("forged receipt touched contact: %v %+v", err, ct)
	}
}
