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

// Herald event callback receipt (HERALD-5): the receipt endpoint parses the
// §13.5 shape natively ({event_id: "cb-...", app, kind, at, delivery: {...}})
// and verifies the X-Herald-Signature HMAC; the HERALD-2 legacy shape
// ({event_id: int64, ...}) is still accepted by the dual-shape dispatch.

// callbackTestSecret is the Herald callback verification secret used by tests
// (32-byte convention, same value as already used in callback_test.go).
const callbackTestSecret = "callback-secret-0123456789abcdef"

// newSecretRouter builds a router carrying the Herald callback verification
// secret: since P2 the receipt endpoint verifies the signature by default, so
// any case POSTing to /api/internal/event-results must be built through this
// (or newCallbackRouter) to get a secret-configured instance.
func newSecretRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	return newTestRouterCfg(t, func(c *config.Config) {
		c.HeraldCallbackSecret = callbackTestSecret
	})
}

// newCallbackRouter builds a router with custom config: an instance with the callback verification secret set.
func newCallbackRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	return newSecretRouter(t)
}

// heraldSign signs the herald-side way: sha256=<hex HMAC-SHA256(secret, body)>.
func heraldSign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// postEventResult POSTs to the receipt endpoint with an optional signature.
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

// doSignedJSON sends a JSON POST carrying X-Herald-Signature: the receipt
// endpoint verifies the signature by default (P2), so any case exercising the
// success path of /api/internal/event-results must be signed with the test secret.
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

// doSignedRawBody sends a signed raw-byte body POST (for cases such as broken
// JSON that an encoder would launder into a legal shape); body-read failures
// (errReader) only need doRawBody, since the body is read before verification.
func doSignedRawBody(t *testing.T, r *gin.Engine, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Herald-Signature", heraldSign(callbackTestSecret, body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// §13.5 callback payload boilerplate: delivery.event_id carries the ferry outbox id back (as a numeric string).
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

// TestEventResultHeraldCallback covers §13.5 native parsing (HERALD-5):
// signature pass/fail, success/failed mapped to sent/failed (reason stored in
// detail), legacy shape still accepted by the dual-shape dispatch, non-numeric
// event_id, missing fields, unsubscribe ack, and no verification without a
// configured secret (existing channels stay intact).
func TestEventResultHeraldCallback(t *testing.T) {
	r, db := newCallbackRouter(t)
	if err := db.Create(&storage.Event{ID: 3001, Kind: "notice", Severity: "info", Title: "t", Target: "user:7", Status: "sent", OccurredAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	// (1) signature passes: §13.5 success -> EventDelivery sent
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

	// (2) signature fails: tampered signature 401
	bad := postEventResult(t, r, "wrong-secret", callbackPayload("3001", "success", ""))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", bad.Code)
	}
	// missing signature header (mandatory when a secret is configured) is also 401
	bare := httptest.NewRequest("POST", "/api/internal/event-results", bytes.NewReader(callbackPayload("3001", "success", "")))
	bare.Header.Set("Content-Type", "application/json")
	bareRec := httptest.NewRecorder()
	r.ServeHTTP(bareRec, bare)
	if bareRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing signature: %d", bareRec.Code)
	}

	// (3) failed -> failure reason lands in detail; touch-job linkage (stale)
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

	// (4) legacy shape (HERALD-2): accepted with a correct signature — the dual-shape dispatch keeps the flow alive
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

	// (5) non-numeric delivery.event_id: 400
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", callbackPayload("cb-8f2c", "success", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric event id: %d %s", rec.Code, rec.Body)
	}

	// (6) missing fields: invalid delivery.status / missing channel, 400 each
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", callbackPayload("3001", "pending", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status: %d", rec.Code)
	}
	noChan := []byte(`{"event_id":"cb-x","app":"ferry","kind":"delivery_result","at":"2026-10-08T00:00:00Z","delivery":{"event_id":"3001","status":"success"}}`)
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", noChan)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing channel: %d", rec.Code)
	}

	// (7) unsubscribe (no delivery): the ack is ignored to stop herald retries from flooding
	unsub := []byte(`{"event_id":"cb-1","app":"ferry","kind":"unsubscribe","at":"2026-10-08T00:00:00Z","unsubscribe":{"audience_id":"user.7","category":"notice","channel":"email","relation_type":"surface"}}`)
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", unsub)
	if rec.Code != http.StatusOK {
		t.Fatalf("unsubscribe ack: %d %s", rec.Code, rec.Body)
	}

	// (8) instance with no secret configured (P2 requires verification by
	// default): both shapes are rejected — without a secret there is no way to
	// tell forged receipts apart, so they are not accepted by default, and no
	// receipt row is left behind.
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

	// (9) non-existent event: 404 (same behavior for both shapes)
	rec = postEventResult(t, r, "callback-secret-0123456789abcdef", callbackPayload("9999", "success", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing event: %d", rec.Code)
	}
}

// TestEventResultSecretlessRejected covers the P2 fix for "event-results not
// verified by default": the receipt endpoint is a public route in the open
// group, so an instance without HeraldCallbackSecret configured must not accept
// any callback (verification is mandatory by default) — before the fix an empty
// secret let everything through and forged receipts (including touch-linkage
// side effects) were accepted by default; after the fix a secretless instance
// returns 401 for both shapes and leaves no receipt row.
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

	// §13.5 callback and legacy shape: a secretless instance returns 401 in all cases, touch-linkage side effects never fire.
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
