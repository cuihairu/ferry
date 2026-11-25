package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPanelContact covers contact binding (TOUCH-1): at least one field
// required, read-back, rebind clearing the stale marker, the routine email
// opt-out switch; 404 with no credential.
func TestPanelContact(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	token := panelToken(t, r, "contact-user")

	// Unbound: read-back is an all-empty row (zero-value struct; the frontend uses it to decide "not ready")
	rec := doPanel(t, r, token, "GET", "/api/panel/contact", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get contact: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Email         string `json:"email"`
		TgChatID      string `json:"tg_chat_id"`
		RoutineEmails bool   `json:"routine_emails"`
		Stale         bool   `json:"stale"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Email != "" || out.TgChatID != "" || out.RoutineEmails {
		t.Fatalf("expected empty contact, got %+v", out)
	}

	// Both empty → 400
	if rec := doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty put: %d %s", rec.Code, rec.Body)
	}
	// Bad email format → 400
	if rec := doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{"email": "nope"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad email: %d", rec.Code)
	}

	// Bind TG only: routine_emails defaults to true on first bind
	rec = doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{"tg_chat_id": " 10086 "})
	if rec.Code != http.StatusOK {
		t.Fatalf("bind tg: %d %s", rec.Code, rec.Body)
	}
	var uid uint
	if err := db.Model(&storage.User{}).Where("username = ?", "contact-user").Pluck("id", &uid).Error; err != nil || uid == 0 {
		t.Fatalf("find user: %v %d", err, uid)
	}
	var row storage.UserContact
	if err := db.First(&row, uid).Error; err != nil {
		t.Fatalf("load contact: %v", err)
	}
	if row.TgChatID != "10086" || !row.RoutineEmails || row.BoundAt.IsZero() {
		t.Fatalf("unexpected contact: %+v", row)
	}

	// Rebind email: stale and the failure counter are cleared, BoundAt refreshed
	db.Model(&row).Updates(map[string]any{"stale": true, "fail_streak": 2})
	oldBound := row.BoundAt
	if rec := doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{"email": "a@b.c"}); rec.Code != http.StatusOK {
		t.Fatalf("rebind: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&row, uid).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.Email != "a@b.c" || row.Stale || row.FailStreak != 0 || !row.BoundAt.After(oldBound) {
		t.Fatalf("rebind not reset: %+v", row)
	}

	// Routine email opt-out switch: a preference-only change is not a rebind (BoundAt untouched)
	bound := row.BoundAt
	if rec := doPanel(t, r, token, "PUT", "/api/panel/contact", map[string]any{"email": "a@b.c", "routine_emails": false}); rec.Code != http.StatusOK {
		t.Fatalf("unsubscribe: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&row, uid).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.RoutineEmails || !row.BoundAt.Equal(bound) {
		t.Fatalf("pref change leaked as rebind: %+v", row)
	}

	// Read-back matches the preferences
	rec = doPanel(t, r, token, "GET", "/api/panel/contact", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Email != "a@b.c" || out.RoutineEmails {
		t.Fatalf("readback mismatch: %+v", out)
	}

	// No credential → 404
	if rec := doPanel(t, r, "", "GET", "/api/panel/contact", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d", rec.Code)
	}
}

// TestContactStaleLoop covers the delivery-failure rebind loop (TOUCH-5): a
// failed touch-event receipt marks the job failed + the contact stale + an
// in-app notice (one per day); three consecutive failures escalate to a contact
// event and clear the counter; a sent receipt clears and recovers it; receipts
// for non-touch events do not propagate.
func TestContactStaleLoop(t *testing.T) {
	r, db := newSecretRouter(t)
	panelToken(t, r, "stale-user")
	var uid uint
	if err := db.Model(&storage.User{}).Where("username = ?", "stale-user").Pluck("id", &uid).Error; err != nil || uid == 0 {
		t.Fatalf("find user: %v %d", err, uid)
	}
	if err := db.Create(&storage.UserContact{
		UserID: uid, Email: "dead@x.c", RoutineEmails: true, BoundAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	// One already-dispatched touch job (mimicking the TOUCH-4 dispatched state)
	job := storage.TouchJob{
		UserID: int64(uid), Channel: "email", Kind: "bill",
		Payload: `{"title":"t","body":"b"}`, Status: "sent", EventID: 5001, CreatedAt: time.Now(),
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	// The receipt endpoint first checks the event row exists; add the outbox row
	if err := db.Create(&storage.Event{ID: 5001, Kind: "bill", Target: herald.TargetUser(int64(uid)),
		Status: "sent", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	fail := func(eventID int64) {
		rec := doSignedJSON(t, r, "/api/internal/event-results", map[string]any{
			"event_id": eventID, "channel": "email", "status": "failed", "detail": "smtp bounce",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("result: %d %s", rec.Code, rec.Body)
		}
	}
	fail(5001)

	var ct storage.UserContact
	if err := db.First(&ct, uid).Error; err != nil {
		t.Fatal(err)
	}
	if !ct.Stale || ct.FailStreak != 1 {
		t.Fatalf("stale not marked: %+v", ct)
	}
	var job2 storage.TouchJob
	if err := db.First(&job2, job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if job2.Status != "failed" || job2.Error != "smtp bounce" {
		t.Fatalf("job not failed: %+v", job2)
	}
	var notices int64
	db.Model(&storage.Notification{}).Where("user_id = ? AND title = ?", uid, "联系方式已失效").Count(&notices)
	if notices != 1 {
		t.Fatalf("stale notice = %d, want 1", notices)
	}
	// A repeat failure the same day does not spam another in-app notice
	fail(5001)
	db.Model(&storage.Notification{}).Where("user_id = ? AND title = ?", uid, "联系方式已失效").Count(&notices)
	if notices != 1 {
		t.Fatalf("stale notice re-sent same day: %d", notices)
	}

	// The third failure hits the threshold: escalate a contact event + clear the counter
	fail(5001)
	var ct2 storage.UserContact
	if err := db.First(&ct2, uid).Error; err != nil {
		t.Fatal(err)
	}
	if ct2.FailStreak != 0 || !ct2.Stale {
		t.Fatalf("escalation not reset: %+v", ct2)
	}
	var escalation int64
	db.Model(&storage.Event{}).Where("kind = ? AND target = ?", "contact", herald.TargetUser(int64(uid))).Count(&escalation)
	if escalation != 1 {
		t.Fatalf("escalation events = %d, want 1", escalation)
	}

	// A sent receipt recovers: stale is cleared
	rec := doSignedJSON(t, r, "/api/internal/event-results", map[string]any{
		"event_id": 5001, "channel": "email", "status": "sent", "detail": "",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("sent result: %d", rec.Code)
	}
	var ct3 storage.UserContact
	if err := db.First(&ct3, uid).Error; err != nil {
		t.Fatal(err)
	}
	if ct3.Stale || ct3.FailStreak != 0 {
		t.Fatalf("sent not recovered: %+v", ct3)
	}
	var job3 storage.TouchJob
	db.First(&job3, job.ID)
	if job3.Status != "sent" || job3.Error != "" {
		t.Fatalf("job not recovered: %+v", job3)
	}

	// A non-touch event (no touch_jobs row) receipt only leaves a trace; no propagation
	if err := db.Create(&storage.Event{ID: 9999, Kind: "region_fault", Target: "admin",
		Status: "sent", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doSignedJSON(t, r, "/api/internal/event-results", map[string]any{
		"event_id": 9999, "channel": "email", "status": "failed", "detail": "x",
	}); rec.Code != http.StatusOK {
		t.Fatalf("non-touch result: %d", rec.Code)
	}
	var after int64
	db.Model(&storage.UserContact{}).Where("user_id = ? AND stale = ?", uid, true).Count(&after)
	if after != 0 {
		t.Fatalf("non-touch leaked stale: %d", after)
	}
}
