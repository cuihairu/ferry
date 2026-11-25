package handler

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Batch-thirty-five gap coverage: event outbox (HERALD-1) error semantics — list with a legal limit taking
// effect / channel filter / fetch failure / status-count failure, retry with a bad id and with a persistence
// failure, receipt body-read failure / bad JSON in both shapes / persistence failure, dashboard bad limit in
// two forms plus a legal limit / fetch failure / aggregation failure / degraded health mark, self-test
// persistence failure, sample kind-list failure and skipping a kind whose fetch fails.
// Gaps left: none.

// isEventModel reports whether the statement lands on the event outbox table.
func isEventModel(v any) bool {
	switch v.(type) {
	case *storage.Event, []storage.Event, *[]storage.Event:
		return true
	}
	return false
}

// isEventDeliveryModel reports whether the statement lands on the channel receipt table.
func isEventDeliveryModel(v any) bool {
	switch v.(type) {
	case *storage.EventDelivery, []storage.EventDelivery, *[]storage.EventDelivery:
		return true
	}
	return false
}

// seedEventRow writes one event row directly and returns its ID.
func seedEventRow(t *testing.T, db *gorm.DB, kind, status string) int64 {
	t.Helper()
	row := storage.Event{Kind: kind, Severity: "info", Title: "t", Body: "b", Status: status}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return row.ID
}

// doRawBody sends a request with a raw byte body (no JSON encoding, no credentials): the receipt
// endpoint sits in the open group, and bad JSON must not be washed into a legal shape by an encoder.
func doRawBody(t *testing.T, r *gin.Engine, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// errReader errors on every read: dedicated to the body-read failure branch.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read fail") }

// TestListEventsErrors list: a legal limit takes effect, channel filter, fetch failure and status
// count failure.
func TestListEventsErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedEventRow(t, db, "l-k", herald.StatusPending)
	if rec := doJSON(t, r, http.MethodGet, "/api/events?limit=5", nil); rec.Code != http.StatusOK {
		t.Fatalf("valid limit: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodGet, "/api/events?kind=l-k&limit=1", nil); rec.Code != http.StatusOK {
		t.Fatalf("kind filter: %d %s", rec.Code, rec.Body)
	}

	// List fetch failure (Find goes through gorm:query).
	r, db = newTestRouterWithDB(t)
	failQueryOn(t, db, "test:event_find", isEventModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/events", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}

	// Status count failure (Count is an aggregation Scan, goes through gorm:row).
	r, db = newTestRouterWithDB(t)
	seedEventRow(t, db, "c-k", herald.StatusSent)
	failRowOn(t, db, "test:event_count", isEventModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/events", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("count: %d %s", rec.Code, rec.Body)
	}
}

// TestRetryEventErrors retry: bad id and persistence failure.
func TestRetryEventErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/events/abc/retry", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	id := seedEventRow(t, db, "r-k", herald.StatusFailed)
	failUpdateOn(t, db, "test:event_retry", isEventModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/events/"+strconv.FormatUint(uint64(id), 10)+"/retry", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("updates: %d %s", rec.Code, rec.Body)
	}
}

// TestEventResultErrors receipt: body-read failure and bad JSON in both shapes, persistence failure.
func TestEventResultErrors(t *testing.T) {
	r, _ := newSecretRouter(t)
	if rec := doRawBody(t, r, http.MethodPost, "/api/internal/event-results", errReader{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("read body: %d %s", rec.Code, rec.Body)
	}
	// Entirely bad JSON: the shape-probe stage fails right away (after signature passes, it lands on JSON parse failure).
	if rec := doSignedRawBody(t, r, "/api/internal/event-results",
		[]byte("{not json")); rec.Code != http.StatusBadRequest {
		t.Fatalf("probe json: %d %s", rec.Code, rec.Body)
	}
	// Legacy shape (numeric event_id) with a mismatched field type: probe stage passes, the second unwrap fails.
	if rec := doSignedRawBody(t, r, "/api/internal/event-results",
		[]byte(`{"event_id":1.5}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("legacy json: %d %s", rec.Code, rec.Body)
	}
	// Callback shape (string event_id) with a mismatched field type: the callback unwrap fails.
	if rec := doSignedRawBody(t, r, "/api/internal/event-results",
		[]byte(`{"event_id":"x","kind":5}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("callback json: %d %s", rec.Code, rec.Body)
	}

	// Receipt persistence failure (the event exists, the receipt table is fault-injected).
	r, db := newSecretRouter(t)
	id := seedEventRow(t, db, "d-k", herald.StatusPending)
	failCreateOn(t, db, "test:delivery_create", isEventDeliveryModel)
	if rec := doSignedRawBody(t, r, "/api/internal/event-results",
		[]byte(`{"event_id":`+strconv.FormatUint(uint64(id), 10)+`,"channel":"chan","status":"sent"}`)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create delivery: %d %s", rec.Code, rec.Body)
	}
}

// TestListEventDeliveriesErrors dashboard: bad limit in two forms, a legal limit, fetch failure,
// aggregation failure and the degraded health mark.
func TestListEventDeliveriesErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, q := range []string{"?limit=abc", "?limit=0", "?limit=201"} {
		if rec := doJSON(t, r, http.MethodGet, "/api/events/deliveries"+q, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("limit %s: %d %s", q, rec.Code, rec.Body)
		}
	}
	if rec := doJSON(t, r, http.MethodGet, "/api/events/deliveries?limit=5", nil); rec.Code != http.StatusOK {
		t.Fatalf("valid limit: %d %s", rec.Code, rec.Body)
	}

	// Receipt-row fetch failure (Find goes through gorm:query).
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:delivery_find", isEventDeliveryModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/events/deliveries", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}

	// Channel aggregation failure (GROUP BY Scan goes through gorm:row).
	r, db = newTestRouterWithDB(t)
	failRowOn(t, db, "test:delivery_agg", isEventDeliveryModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/events/deliveries", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("agg: %d %s", rec.Code, rec.Body)
	}

	// Degraded health: one channel with both sent and failed → degraded, and it returns the latest failure detail.
	r, db = newTestRouterWithDB(t)
	for _, st := range []string{herald.StatusSent, herald.StatusFailed} {
		if err := db.Create(&storage.EventDelivery{EventID: 1, Channel: "mail", Status: st,
			Detail: "boom", At: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	rec := doJSON(t, r, http.MethodGet, "/api/events/deliveries", nil)
	if rec.Code != http.StatusOK || !containsStr(rec.Body.String(), `"degraded"`) ||
		!containsStr(rec.Body.String(), "boom") {
		t.Fatalf("degraded health: %d %s", rec.Code, rec.Body)
	}
}

// TestTestEventError channel self-test persistence failure 500.
func TestTestEventError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failCreateOn(t, db, "test:event_emit", isEventModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/events/test", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("emit: %d %s", rec.Code, rec.Body)
	}
}

// TestListEventSamplesErrors samples: kind-list failure and skipping a kind whose fetch fails (no
// half-finished sample list is served).
func TestListEventSamplesErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:event_pluck", isEventModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/events/samples", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("pluck: %d %s", rec.Code, rec.Body)
	}

	// The kind list (query 1) is allowed through; the sample fetch (query 2) is fault-injected → continue skips it.
	r, db = newTestRouterWithDB(t)
	seedEventRow(t, db, "s-k", herald.StatusSent)
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:event_sample_first", func(tx *gorm.DB) {
		if isEventModel(tx.Statement.Model) || isEventModel(tx.Statement.Dest) {
			seen++
			if seen == 2 {
				tx.AddError(errors.New("db down"))
			}
		}
	})
	rec := doJSON(t, r, http.MethodGet, "/api/events/samples", nil)
	if rec.Code != http.StatusOK || containsStr(rec.Body.String(), "s-k") {
		t.Fatalf("skip failed sample: %d %s", rec.Code, rec.Body)
	}
}
