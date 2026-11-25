package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch-24 gap fill: the notification center's various 400/500 paths and the
// panel's unauthenticated stance - validation failures (bad binding/too
// long/out-of-range limit/missing field/non-numeric id), DB faults (closed
// handle and callback error injection targeted per table), the follow-up
// queries in the counting pass failing, and the threshold normalization's
// fallback for legacy bad rows.

// notifUserID fetches the seeded user row's id (panel endpoints check
// ownership by id).
func notifUserID(t *testing.T, db *gorm.DB, username string) uint {
	t.Helper()
	var u storage.User
	if err := db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatalf("find user %s: %v", username, err)
	}
	return u.ID
}

// notifRow writes one normal notification and returns its row id.
func notifRow(t *testing.T, db *gorm.DB, uid uint, title string) int64 {
	t.Helper()
	row := storage.Notification{UserID: int64(uid), Type: storage.NotifSystem,
		Title: title, Body: "正文", CreatedAt: time.Now()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create notification: %v", err)
	}
	return row.ID
}

// notifAnchor writes an announcement anchor row (user_id=0) plus one attached
// user row, to feed the announcement list's counting pass.
func notifAnchor(t *testing.T, db *gorm.DB, uid uint) int64 {
	t.Helper()
	a := storage.Notification{UserID: 0, Type: storage.NotifAnnouncement,
		Title: "公告", Audience: "all", CreatedAt: time.Now()}
	if err := db.Create(&a).Error; err != nil {
		t.Fatalf("create anchor: %v", err)
	}
	u := storage.Notification{UserID: int64(uid), Type: storage.NotifAnnouncement,
		Title: "公告", AnchorID: a.ID, CreatedAt: time.Now()}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user row: %v", err)
	}
	return a.ID
}

// closeSQL closes the underlying connection: every later DB operation errors.
func closeSQL(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}

// isNotifModel tells whether a statement targets the notification table: the
// Find chain's Model is a slice pointer, Count/First chains use a single-element
// pointer - recognize both shapes.
func isNotifModel(v any) bool {
	switch v.(type) {
	case *storage.Notification, []storage.Notification, *[]storage.Notification:
		return true
	}
	return false
}

// failNotifQueryAt fails the n-th (1-based) query against the notification
// table: in the counting pass the anchor list is followed by two COUNTs, so
// order is used to aim.
func failNotifQueryAt(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:notif_query_at", func(tx *gorm.DB) {
		if !isNotifModel(tx.Statement.Model) {
			return
		}
		seen++
		if seen == n {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failNotifUpdates makes notification-row updates fail (mark-read/read-all).
func failNotifUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:notif_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Notification); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failNotifDeletes makes notification-row deletes fail (inside the
// transaction, so the cascade withdrawal fails too).
func failNotifDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:notif_delete", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Notification); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failUserUpdates makes user-row updates fail (notification preference writeback).
func failUserUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:user_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.User); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

// setWarnPercent writes the threshold straight to the DB (the PUT surface
// validates 1-100, so legacy bad rows can only be made from the DB side).
func setWarnPercent(t *testing.T, db *gorm.DB, uid uint, v int) {
	t.Helper()
	if err := db.Model(&storage.User{}).Where("id = ?", uid).
		Update("traffic_warn_percent", v).Error; err != nil {
		t.Fatalf("set warn percent: %v", err)
	}
}

// TestCreateAnnouncementErrors covers announcement creation validation and
// fan-out failure paths.
func TestCreateAnnouncementErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body any
		msg  string
	}{
		{"bind", "not json", ""},
		{"title", map[string]any{"title": strings.Repeat("标", 130)}, "标题过长"},
		{"body", map[string]any{"title": "标", "body": strings.Repeat("正", 520)}, "正文过长"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestRouterWithDB(t)
			rec := doJSON(t, r, http.MethodPost, "/api/notifications/announcement", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400: %s", rec.Code, rec.Body)
			}
			if tc.msg != "" && !strings.Contains(rec.Body.String(), tc.msg) {
				t.Fatalf("body = %s, want %q", rec.Body, tc.msg)
			}
		})
	}

	// Fan-out DB fault: internal errors do not leak details.
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	rec := doJSON(t, r, http.MethodPost, "/api/notifications/announcement",
		map[string]any{"title": "标"})
	if rec.Code != http.StatusInternalServerError ||
		!strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body)
	}
}

// TestListAnnouncementsErrors covers announcement list limit validation and each
// of the three queries failing on its own.
func TestListAnnouncementsErrors(t *testing.T) {
	// Out-of-range limit (the default 50 is in range; valid values take the
	// clamping branch).
	r, _ := newTestRouterWithDB(t)
	for _, limit := range []string{"abc", "0", "201"} {
		rec := doJSON(t, r, http.MethodGet, "/api/notifications/announcements?limit="+limit, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "1-200") {
			t.Fatalf("limit=%s: code=%d body=%s", limit, rec.Code, rec.Body)
		}
	}
	if rec := doJSON(t, r, http.MethodGet, "/api/notifications/announcements?limit=1", nil); rec.Code != http.StatusOK {
		t.Fatalf("limit=1: %d %s", rec.Code, rec.Body)
	}

	// Anchor list query fails.
	r, db := newTestRouterWithDB(t)
	panelToken(t, r, "anon-a")
	notifAnchor(t, db, notifUserID(t, db, "anon-a"))
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/notifications/announcements", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("anchors: %d %s", rec.Code, rec.Body)
	}

	// Sent-count query fails (the 2nd query).
	r, db = newTestRouterWithDB(t)
	panelToken(t, r, "anon-b")
	notifAnchor(t, db, notifUserID(t, db, "anon-b"))
	failNotifQueryAt(t, db, 2)
	if rec := doJSON(t, r, http.MethodGet, "/api/notifications/announcements", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("sent count: %d %s", rec.Code, rec.Body)
	}

	// Read-count query fails (the 3rd query).
	r, db = newTestRouterWithDB(t)
	_ = panelToken(t, r, "anon-c")
	notifAnchor(t, db, notifUserID(t, db, "anon-c"))
	failNotifQueryAt(t, db, 3)
	if rec := doJSON(t, r, http.MethodGet, "/api/notifications/announcements", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("read count: %d %s", rec.Code, rec.Body)
	}
}

// TestResendAnnouncementErrors covers resend id validation and non-conflict DB faults.
func TestResendAnnouncementErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, id := range []string{"abc", ""} {
		rec := doJSON(t, r, http.MethodPost, "/api/notifications/announcements/"+id+"/resend", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("id=%q: code=%d body=%s", id, rec.Code, rec.Body)
		}
	}

	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	rec := doJSON(t, r, http.MethodPost, "/api/notifications/announcements/1/resend", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("db error: code=%d body=%s", rec.Code, rec.Body)
	}
}

// TestListNotificationsErrors covers the full listing's limit/filter rules and query failures.
func TestListNotificationsErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, limit := range []string{"abc", "0", "501"} {
		rec := doJSON(t, r, http.MethodGet, "/api/notifications?limit="+limit, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "1-500") {
			t.Fatalf("limit=%s: code=%d body=%s", limit, rec.Code, rec.Body)
		}
	}

	// Valid limit and user_id filter.
	r, db := newTestRouterWithDB(t)
	panelToken(t, r, "list-u")
	notifRow(t, db, notifUserID(t, db, "list-u"), "通知")
	rec := doJSON(t, r, http.MethodGet,
		"/api/notifications?limit=100&user_id="+strconv.FormatUint(uint64(notifUserID(t, db, "list-u")), 10)+"&type=system", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filters: %d %s", rec.Code, rec.Body)
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d err=%v", len(rows), err)
	}

	// Query fails.
	r, db = newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/notifications", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteNotificationErrors covers deletion id validation, lookup failure, and
// delete failure inside the transaction.
func TestDeleteNotificationErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doJSON(t, r, http.MethodDelete, "/api/notifications/abc", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	rec = doJSON(t, r, http.MethodDelete, "/api/notifications/1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}

	// Delete fails inside the transaction: the lookup succeeds but the cascade
	// withdrawal errors.
	r, db = newTestRouterWithDB(t)
	panelToken(t, r, "del-u")
	notifAnchor(t, db, notifUserID(t, db, "del-u"))
	failNotifDeletes(t, db)
	rec = doJSON(t, r, http.MethodDelete, "/api/notifications/1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelNotificationErrors covers the panel side's unauthenticated 404, limit
// validation, and query/update failures.
func TestPanelNotificationErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// No token: all six read/write endpoints return not-found.
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/panel/notifications"},
		{http.MethodGet, "/api/panel/notifications/unread-count"},
		{http.MethodPost, "/api/panel/notifications/read-all"},
		{http.MethodPost, "/api/panel/notifications/1/read"},
		{http.MethodGet, "/api/panel/notify-prefs"},
		{http.MethodPut, "/api/panel/notify-prefs"},
	} {
		rec := doPanel(t, r, "", tc.method, tc.path, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s without token: %d %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}

	// Out-of-range limit and valid clamping; list and unread-count queries fail.
	tok := panelToken(t, r, "pnl-limit")
	if rec := doPanel(t, r, tok, http.MethodGet,
		"/api/panel/notifications?limit=abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("limit: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, tok, http.MethodGet,
		"/api/panel/notifications?limit=10", nil); rec.Code != http.StatusOK {
		t.Fatalf("limit=10: %d %s", rec.Code, rec.Body)
	}
	r, db := newTestRouterWithDB(t)
	tok = panelToken(t, r, "pnl-db")
	failNotifQueryAt(t, db, 1)
	if rec := doPanel(t, r, tok, http.MethodGet, "/api/panel/notifications", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	r, db = newTestRouterWithDB(t)
	tok = panelToken(t, r, "pnl-db2")
	failNotifQueryAt(t, db, 1)
	if rec := doPanel(t, r, tok, http.MethodGet,
		"/api/panel/notifications/unread-count", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("unread: %d %s", rec.Code, rec.Body)
	}

	// Mark-read: non-numeric id 400; update failure 500.
	r, db = newTestRouterWithDB(t)
	tok = panelToken(t, r, "pnl-read")
	if rec := doPanel(t, r, tok, http.MethodPost,
		"/api/panel/notifications/abc/read", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("read bad id: %d %s", rec.Code, rec.Body)
	}
	notifRow(t, db, notifUserID(t, db, "pnl-read"), "通知")
	failNotifUpdates(t, db)
	if rec := doPanel(t, r, tok, http.MethodPost, "/api/panel/notifications/1/read", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("read update: %d %s", rec.Code, rec.Body)
	}

	// Read-all: update failure.
	r, db = newTestRouterWithDB(t)
	tok = panelToken(t, r, "pnl-all")
	failNotifUpdates(t, db)
	if rec := doPanel(t, r, tok, http.MethodPost, "/api/panel/notifications/read-all", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("read-all: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelNotifyPrefsErrors covers preferences: missing field, update failure,
// and the threshold fallback for legacy bad rows.
func TestPanelNotifyPrefsErrors(t *testing.T) {
	// Missing field 400 (nothing updatable); both booleans update through (the
	// notify_traffic field branch).
	r, db := newTestRouterWithDB(t)
	tok := panelToken(t, r, "pref-err")
	rec := doPanel(t, r, tok, http.MethodPut, "/api/panel/notify-prefs", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}
	rec = doPanel(t, r, tok, http.MethodPut, "/api/panel/notify-prefs",
		map[string]any{"notify_expiry": true, "notify_traffic": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("put two: %d %s", rec.Code, rec.Body)
	}

	// Bad binding 400.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/panel/notify-prefs", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", w.Code, w.Body)
	}

	// Legacy bad-row threshold falls back to 80 (out-of-range values written
	// from the DB side, legacy data the API validation cannot catch).
	// Must run before error injection: the injected update failure would
	// block this step's direct DB write.
	uid := notifUserID(t, db, "pref-err")
	for _, v := range []int{0, 200} {
		setWarnPercent(t, db, uid, v)
		var prefs struct {
			WarnPercent int `json:"traffic_warn_percent"`
		}
		rec = doPanel(t, r, tok, http.MethodGet, "/api/panel/notify-prefs", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("get prefs: %d %s", rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &prefs); err != nil {
			t.Fatal(err)
		}
		if prefs.WarnPercent != 80 {
			t.Fatalf("warn percent for %d = %d, want 80", v, prefs.WarnPercent)
		}
	}

	// Update failure 500.
	failUserUpdates(t, db)
	rec = doPanel(t, r, tok, http.MethodPut, "/api/panel/notify-prefs",
		map[string]any{"notify_expiry": false})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
}
