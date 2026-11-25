package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/announce"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// feedText fetches the /feed.xml text (a public endpoint, used to assert window filtering).
func feedText(t *testing.T, r *gin.Engine) string {
	t.Helper()
	rec := doJSON(t, r, "GET", "/feed.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("feed: %d %s", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// TestAnnouncementWindow covers P2-5 (1): scheduled publishing (a future
// starts_at only stores the anchor row, fanned out by RunDue when due) and
// takedown (after ends_at the panel list and unread count hide it, and it
// leaves the RSS).
func TestAnnouncementWindow(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	token := panelToken(t, r, "win-u")

	// Scheduled publishing: a future starts_at -> no user rows (created=0), not in the RSS.
	start := time.Now().Add(time.Hour).Truncate(time.Second)
	end := start.Add(2 * time.Hour)
	rec := doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{
		"title": "定时公告", "body": "到点可见",
		"starts_at": start.Format(time.RFC3339), "ends_at": end.Format(time.RFC3339),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("scheduled announcement: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Created   int64 `json:"created"`
		Scheduled bool  `json:"scheduled"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Created != 0 || !out.Scheduled {
		t.Fatalf("scheduled resp = %+v, want created=0 scheduled=true", out)
	}
	if strings.Contains(feedText(t, r), "定时公告") {
		t.Fatal("future announcement leaked into feed")
	}
	var userRows int64
	if err := db.Model(&storage.Notification{}).Where("user_id != 0 AND type = ?", storage.NotifAnnouncement).
		Count(&userRows).Error; err != nil {
		t.Fatal(err)
	}
	if userRows != 0 {
		t.Fatalf("user rows before due = %d, want 0", userRows)
	}

	// starts_at > ends_at validation is 400.
	if rec := doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{
		"title": "坏窗口", "starts_at": end.Format(time.RFC3339), "ends_at": start.Format(time.RFC3339),
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window: %d", rec.Code)
	}

	// Fan-out when due (announce.RunDue inside the notifyscan Sweep).
	n, err := announce.RunDue(db, start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("RunDue fanned = %d, want 1", n)
	}
	var rows []storage.Notification
	if err := db.Where("user_id != 0 AND type = ?", storage.NotifAnnouncement).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].AnchorID == 0 || rows[0].EndsAt == nil {
		t.Fatalf("user rows after due = %+v", rows)
	}
	// The anchor row's fanout_at is written back; a repeat RunDue fans out nothing.
	var anchor storage.Notification
	if err := db.Where("user_id = ? AND type = ?", 0, storage.NotifAnnouncement).First(&anchor).Error; err != nil {
		t.Fatal(err)
	}
	if anchor.FanoutAt == nil {
		t.Fatal("anchor fanout_at not set")
	}
	if n, _ := announce.RunDue(db, start.Add(2*time.Minute)); n != 0 {
		t.Fatalf("second RunDue fanned = %d, want 0", n)
	}
	// Inside the window: visible in the panel, one unread, present in the RSS.
	rec = doPanel(t, r, token, "GET", "/api/panel/notifications", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 1 {
		t.Fatalf("panel list in window = %d rows, want 1", len(rows))
	}
	if !strings.Contains(feedText(t, r), "定时公告") {
		t.Fatal("in-window announcement missing from feed")
	}

	// Takedown: ends_at in the past -> hidden from the panel list and unread count,
	// gone from the RSS; the rows are kept.
	if err := db.Model(&storage.Notification{}).Where("id IN (?)",
		db.Model(&storage.Notification{}).Select("id").Where("type = ? AND title = ?", storage.NotifAnnouncement, "定时公告"),
	).Update("ends_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	rec = doPanel(t, r, token, "GET", "/api/panel/notifications", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 0 {
		t.Fatalf("panel list after takedown = %d rows, want 0", len(rows))
	}
	rec = doPanel(t, r, token, "GET", "/api/panel/notifications/unread-count", nil)
	var cnt struct {
		Count int64 `json:"count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cnt)
	if cnt.Count != 0 {
		t.Fatalf("unread after takedown = %d, want 0", cnt.Count)
	}
	if strings.Contains(feedText(t, r), "定时公告") {
		t.Fatal("taken-down announcement still in feed")
	}
	var all int64
	if err := db.Model(&storage.Notification{}).Where("type = ?", storage.NotifAnnouncement).Count(&all).Error; err != nil {
		t.Fatal(err)
	}
	if all != 2 { // anchor row + user row are both present, just not visible
		t.Fatalf("rows after takedown = %d, want 2", all)
	}
}

// TestAnnouncementStatsResend covers P2-5 (2)(3): sent-n/read-m stats
// feedback, the external-channel resend position and 6h rate limit, and no
// resend once the window is closed.
func TestAnnouncementStatsResend(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	tokA := panelToken(t, r, "stat-a")
	panelToken(t, r, "stat-b")

	rec := doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{"title": "触达公告"})
	if rec.Code != http.StatusOK {
		t.Fatalf("announcement: %d %s", rec.Code, rec.Body)
	}
	// Stats: 2 sent, 0 read.
	type stat struct {
		ID   int64  `json:"id"`
		Sent int64  `json:"sent"`
		Read int64  `json:"read"`
		Name string `json:"title"`
	}
	getStats := func() []stat {
		rec := doJSON(t, r, "GET", "/api/notifications/announcements", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("stats: %d %s", rec.Code, rec.Body)
		}
		var rows []stat
		_ = json.Unmarshal(rec.Body.Bytes(), &rows)
		return rows
	}
	stats := getStats()
	if len(stats) != 1 || stats[0].Sent != 2 || stats[0].Read != 0 {
		t.Fatalf("stats = %+v, want 1 row sent=2 read=0", stats)
	}
	// One user has read -> read is 1.
	rec = doPanel(t, r, tokA, "GET", "/api/panel/notifications", nil)
	var rows []storage.Notification
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	rec = doPanel(t, r, tokA, "POST", "/api/panel/notifications/"+strconv.FormatInt(rows[0].ID, 10)+"/read", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read: %d %s", rec.Code, rec.Body)
	}
	stats = getStats()
	if stats[0].Read != 1 {
		t.Fatalf("read = %d, want 1", stats[0].Read)
	}

	// Resend: a round of notice events is emitted (one per enabled user; in-app messages are not re-stored).
	var before int64
	db.Model(&storage.Event{}).Where("kind = ?", "notice").Count(&before)
	anchorID := stats[0].ID
	rec = doJSON(t, r, "POST", "/api/notifications/announcements/"+strconv.FormatInt(anchorID, 10)+"/resend", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resend: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Emitted int64 `json:"emitted"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Emitted != 2 {
		t.Fatalf("resend emitted = %d, want 2", out.Emitted)
	}
	var after int64
	db.Model(&storage.Event{}).Where("kind = ?", "notice").Count(&after)
	if after-before != 2 {
		t.Fatalf("notice events delta = %d, want 2", after-before)
	}
	var userRows int64
	db.Model(&storage.Notification{}).Where("user_id != 0 AND type = ?", storage.NotifAnnouncement).Count(&userRows)
	if userRows != 2 {
		t.Fatalf("user rows after resend = %d, want 2 (in-app rows not duplicated)", userRows)
	}

	// Rate limit: a resend within 6h is 429.
	rec = doJSON(t, r, "POST", "/api/notifications/announcements/"+strconv.FormatInt(anchorID, 10)+"/resend", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("resend again: %d, want 429", rec.Code)
	}
	// The stats row's resend_at is written back.
	stats = getStats()
	if stats[0].Sent != 2 || stats[0].Read != 1 {
		t.Fatalf("stats after resend = %+v", stats[0])
	}

	// A closed window does not resend (takedown semantics take precedence).
	if err := db.Model(&storage.Notification{}).Where("id = ?", anchorID).
		Update("ends_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "POST", "/api/notifications/announcements/"+strconv.FormatInt(anchorID, 10)+"/resend", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("resend closed: %d, want 400", rec.Code)
	}
	// A nonexistent announcement is 404.
	rec = doJSON(t, r, "POST", "/api/notifications/announcements/999999/resend", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("resend missing: %d, want 404", rec.Code)
	}
}

// TestAnnouncementAudience covers ops design §1.1 "all/groups": expiring:N only
// fans out to enabled users expiring within N days (expired/no expiry/disabled
// are excluded), a scheduled announcement resolves its audience only when due,
// a resend recomputes from the same audience, and a bad audience is 400.
func TestAnnouncementAudience(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	for _, u := range []string{"aud-near", "aud-far", "aud-none", "aud-expired", "aud-off"} {
		panelToken(t, r, u)
	}
	now := time.Now()
	at := func(d time.Time) *time.Time { t := d; return &t }
	setExp := func(username string, v *time.Time) {
		if err := db.Model(&storage.User{}).Where("username = ?", username).
			Update("expires_at", v).Error; err != nil {
			t.Fatal(err)
		}
	}
	setExp("aud-near", at(now.Add(3*24*time.Hour)))
	setExp("aud-far", at(now.Add(30*24*time.Hour)))
	setExp("aud-expired", at(now.Add(-24*time.Hour)))
	setExp("aud-off", at(now.Add(48*time.Hour)))
	if err := db.Model(&storage.User{}).Where("username = ?", "aud-off").
		Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}

	post := func(title, aud string) *httptest.ResponseRecorder {
		body := map[string]any{"title": title}
		if aud != "" {
			body["audience"] = aud
		}
		return doJSON(t, r, "POST", "/api/notifications/announcement", body)
	}
	created := func(rec *httptest.ResponseRecorder) int64 {
		if rec.Code != http.StatusOK {
			t.Fatalf("announcement: %d %s", rec.Code, rec.Body)
		}
		var out struct {
			Created int64 `json:"created"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Created
	}

	// The default and "all" audiences cover every enabled user (4, disabled ones excluded).
	if n := created(post("全员公告", "")); n != 4 {
		t.Fatalf("default audience created = %d, want 4", n)
	}
	if n := created(post("全员工告B", "all")); n != 4 {
		t.Fatalf("all audience created = %d, want 4", n)
	}
	// expiring:7 only reaches users expiring within 3 days; expiring:30 reaches the 3-day and 30-day ones (expired/no expiry excluded).
	if n := created(post("到期七日", "expiring:7")); n != 1 {
		t.Fatalf("expiring:7 created = %d, want 1", n)
	}
	if n := created(post("到期三十", "expiring:30")); n != 2 {
		t.Fatalf("expiring:30 created = %d, want 2", n)
	}
	// A bad audience is 400.
	for _, bad := range []string{"expiring:0", "expiring:91", "bogus"} {
		if rec := post("坏受众", bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("audience %q: %d, want 400", bad, rec.Code)
		}
	}

	// Scheduled announcement: nothing is created at publish time; when due it fans
	// out along the expiring:7 audience -- users who join after scheduling are
	// included too (the audience is resolved at fan-out time).
	start := now.Add(time.Hour).Truncate(time.Second)
	rec := doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{
		"title": "定时受众", "audience": "expiring:7",
		"starts_at": start.Format(time.RFC3339),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("scheduled audience: %d %s", rec.Code, rec.Body)
	}
	panelToken(t, r, "aud-late")
	setExp("aud-late", at(now.Add(24*time.Hour)))
	n, err := announce.RunDue(db, start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 { // near + late (far out of window, none has no expiry, expired is past, off is disabled)
		t.Fatalf("RunDue audience fanned = %d, want 2", n)
	}

	// A resend recomputes the same audience (the near user has now aged out of the window -> only late remains).
	setExp("aud-near", at(now.Add(-40*24*time.Hour)))
	type stat struct {
		ID       int64   `json:"id"`
		Audience string  `json:"audience"`
		Sent     int64   `json:"sent"`
		FanoutAt *string `json:"fanout_at"`
	}
	rec = doJSON(t, r, "GET", "/api/notifications/announcements?limit=200", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body)
	}
	var stats []stat
	_ = json.Unmarshal(rec.Body.Bytes(), &stats)
	var anchorID int64
	for _, s := range stats {
		if s.Audience == "expiring:7" && s.Sent == 2 {
			anchorID = s.ID
		}
	}
	if anchorID == 0 {
		t.Fatalf("scheduled audience anchor not found in stats: %+v", stats)
	}
	rec = doJSON(t, r, "POST", "/api/notifications/announcements/"+strconv.FormatInt(anchorID, 10)+"/resend", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resend: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Emitted int64 `json:"emitted"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Emitted != 1 {
		t.Fatalf("resend emitted = %d, want 1 (near aged out of window)", out.Emitted)
	}
}

// TestAnnouncementDeleteCascade covers anchor cascading takedown: deleting the
// anchor row also deletes the fanned-out user rows (the announcement vanishes
// from each user's list and unread count); a plain row is still deleted alone.
func TestAnnouncementDeleteCascade(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	panelToken(t, r, "cas-a")
	panelToken(t, r, "cas-b")

	// An all-users announcement -> 1 anchor row + 2 user rows.
	rec := doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{"title": "待撤回"})
	if rec.Code != http.StatusOK {
		t.Fatalf("announcement: %d %s", rec.Code, rec.Body)
	}
	var anchor storage.Notification
	if err := db.Where("user_id = 0 AND title = ?", "待撤回").First(&anchor).Error; err != nil {
		t.Fatal(err)
	}
	var fanout int64
	if err := db.Model(&storage.Notification{}).Where("anchor_id = ?", anchor.ID).Count(&fanout).Error; err != nil {
		t.Fatal(err)
	}
	if fanout != 2 {
		t.Fatalf("fanout rows = %d, want 2", fanout)
	}

	// Deleting the anchor -> the anchor and user rows all vanish (cascading
	// takedown); deleting again is 404.
	if rec := doJSON(t, r, "DELETE", "/api/notifications/"+strconv.FormatInt(anchor.ID, 10), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete anchor: %d %s", rec.Code, rec.Body)
	}
	var left int64
	if err := db.Model(&storage.Notification{}).Where("id = ? OR anchor_id = ?", anchor.ID, anchor.ID).Count(&left).Error; err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("rows after anchor delete = %d, want 0", left)
	}
	if rec := doJSON(t, r, "DELETE", "/api/notifications/"+strconv.FormatInt(anchor.ID, 10), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d, want 404", rec.Code)
	}

	// A plain row is still deleted on its own: create another round, and deleting
	// one user row leaves the rest untouched.
	rec = doJSON(t, r, "POST", "/api/notifications/announcement", map[string]any{"title": "第二轮"})
	if rec.Code != http.StatusOK {
		t.Fatalf("announcement 2: %d %s", rec.Code, rec.Body)
	}
	var userRows []storage.Notification
	if err := db.Where("anchor_id IS NOT NULL AND user_id != 0").Order("id").Find(&userRows).Error; err != nil {
		t.Fatal(err)
	}
	if len(userRows) != 2 {
		t.Fatalf("round2 user rows = %d, want 2", len(userRows))
	}
	if rec := doJSON(t, r, "DELETE", "/api/notifications/"+strconv.FormatInt(userRows[0].ID, 10), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete user row: %d %s", rec.Code, rec.Body)
	}
	var rest int64
	if err := db.Model(&storage.Notification{}).Where("user_id != 0").Count(&rest).Error; err != nil {
		t.Fatal(err)
	}
	if rest != 1 {
		t.Fatalf("user rows after single delete = %d, want 1", rest)
	}
}
