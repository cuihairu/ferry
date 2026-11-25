package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// TestEventsAPI covers the outbox admin side: list with counts, status filter, dead-letter retry and
// non-dead-letter conflict.
func TestEventsAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// One row per status: sent / pending / failed (dead letter)
	now := time.Now()
	rows := []storage.Event{
		{Kind: "region_fault", Severity: herald.SeverityCritical, Title: "华东不可达", Target: "admin", Status: herald.StatusSent, Attempts: 1, OccurredAt: now, CreatedAt: now},
		{Kind: "cert_expiring", Severity: herald.SeverityWarning, Title: "证书 7 天到期", Target: "admin", Status: herald.StatusPending, OccurredAt: now, CreatedAt: now},
		{Kind: "node_blocked", Severity: herald.SeverityCritical, Title: "节点被封", Target: "admin", Status: herald.StatusFailed, Attempts: herald.MaxAttempts, OccurredAt: now, CreatedAt: now},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	failedID := rows[2].ID

	// Full list + counts (pending 2 = pending 1 + failed 1? no — counts are listed per status)
	rec := doJSON(t, r, "GET", "/api/events", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Rows   []storage.Event `json:"rows"`
		Counts struct {
			Pending int64 `json:"pending"`
			Sent    int64 `json:"sent"`
			Failed  int64 `json:"failed"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(out.Rows))
	}
	if out.Counts.Pending != 1 || out.Counts.Sent != 1 || out.Counts.Failed != 1 {
		t.Fatalf("counts = %+v", out.Counts)
	}

	// status filter
	rec = doJSON(t, r, "GET", "/api/events?status=failed", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0].Status != herald.StatusFailed {
		t.Fatalf("failed filter = %+v", out.Rows)
	}

	// limit out of range → 400
	if rec := doJSON(t, r, "GET", "/api/events?limit=0", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=0: %d", rec.Code)
	}

	// Dead-letter retry → back to pending, clearing the next delivery time
	if rec := doJSON(t, r, "POST", "/api/events/"+strconv.FormatInt(failedID, 10)+"/retry", nil); rec.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	var got storage.Event
	if err := db.First(&got, failedID).Error; err != nil || got.Status != herald.StatusPending || got.NextAttemptAt != nil {
		t.Fatalf("after retry = %+v err=%v", got, err)
	}

	// A second retry conflicts (already pending); a missing event is also 409
	if rec := doJSON(t, r, "POST", "/api/events/"+strconv.FormatInt(failedID, 10)+"/retry", nil); rec.Code != http.StatusConflict {
		t.Fatalf("retry pending should 409: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/events/99999/retry", nil); rec.Code != http.StatusConflict {
		t.Fatalf("retry missing should 409: %d", rec.Code)
	}
}

// TestEventResultAPI covers the Herald channel-dispatch receipt (HERALD-2): the receipt is
// persisted to EventDelivery as a record, the event's own status is untouched, plus parameter validation and unknown-event 404.
func TestEventResultAPI(t *testing.T) {
	r, db := newSecretRouter(t)

	now := time.Now()
	ev := storage.Event{Kind: "region_fault", Severity: herald.SeverityCritical, Title: "华东不可达", Target: "admin", Status: herald.StatusSent, Attempts: 1, OccurredAt: now, CreatedAt: now}
	if err := db.Create(&ev).Error; err != nil {
		t.Fatal(err)
	}

	post := func(body gin.H) *httptest.ResponseRecorder {
		return doSignedJSON(t, r, "/api/internal/event-results", body)
	}

	// Normal receipts: one sent and one failed record, event status unchanged
	if rec := post(gin.H{"event_id": ev.ID, "channel": "tg", "status": "sent", "detail": "msg_id 123"}); rec.Code != http.StatusOK {
		t.Fatalf("result sent: %d %s", rec.Code, rec.Body)
	}
	if rec := post(gin.H{"event_id": ev.ID, "channel": "webhook", "status": "failed", "detail": "5xx"}); rec.Code != http.StatusOK {
		t.Fatalf("result failed: %d %s", rec.Code, rec.Body)
	}
	var got storage.Event
	if err := db.First(&got, ev.ID).Error; err != nil || got.Status != herald.StatusSent {
		t.Fatalf("event mutated by result = %+v err=%v", got, err)
	}
	var dls []storage.EventDelivery
	if err := db.Where("event_id = ?", ev.ID).Order("id").Find(&dls).Error; err != nil || len(dls) != 2 {
		t.Fatalf("deliveries = %d err=%v", len(dls), err)
	}
	if dls[0].Channel != "tg" || dls[0].Status != "sent" || dls[0].Detail != "msg_id 123" {
		t.Fatalf("delivery[0] = %+v", dls[0])
	}
	if dls[1].Channel != "webhook" || dls[1].Status != "failed" {
		t.Fatalf("delivery[1] = %+v", dls[1])
	}

	// Validation: missing event_id / missing channel / invalid status → 400
	if rec := post(gin.H{"channel": "tg", "status": "sent"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("no event_id: %d", rec.Code)
	}
	if rec := post(gin.H{"event_id": ev.ID, "status": "sent"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("no channel: %d", rec.Code)
	}
	if rec := post(gin.H{"event_id": ev.ID, "channel": "tg", "status": "pending"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status: %d", rec.Code)
	}
	// Unknown event → 404
	if rec := post(gin.H{"event_id": 424242, "channel": "tg", "status": "sent"}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown event: %d", rec.Code)
	}
}
