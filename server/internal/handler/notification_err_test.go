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

// 二十四批补口：通知中心各 400/500 与面板未鉴权口径——校验失败（坏绑定/
// 超长/limit 越界/缺字段/非数字 id）、库故障（关句柄与回调注错定向各表）、
// 统计回流二次查询失败、以及阈值归一的历史坏行回落。

// notifUserID 取建库用户行 id（面板端点按 id 校验归属）。
func notifUserID(t *testing.T, db *gorm.DB, username string) uint {
	t.Helper()
	var u storage.User
	if err := db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatalf("find user %s: %v", username, err)
	}
	return u.ID
}

// notifRow 落一条普通通知，返回行 id。
func notifRow(t *testing.T, db *gorm.DB, uid uint, title string) int64 {
	t.Helper()
	row := storage.Notification{UserID: int64(uid), Type: storage.NotifSystem,
		Title: title, Body: "正文", CreatedAt: time.Now()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create notification: %v", err)
	}
	return row.ID
}

// notifAnchor 落公告锚点行（user_id=0）并挂一条用户行，供公告列表统计回流。
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

// closeSQL 关掉底层连接：之后任何库操作一律报错。
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

// isNotifModel 判断语句是否落在通知表上：Find 链的 Model 是切片指针，
// Count/First 链是单元素指针，两个形态都要认。
func isNotifModel(v any) bool {
	switch v.(type) {
	case *storage.Notification, []storage.Notification, *[]storage.Notification:
		return true
	}
	return false
}

// failNotifQueryAt 第 n 次（1 起）针对通知表的查询失败：统计回流里锚点列表
// 之后是两次 COUNT，用次序定向。
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

// failNotifUpdates 让通知行的更新失败（标已读/全部已读）。
func failNotifUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:notif_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Notification); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failNotifDeletes 让通知行删除失败（事务内，级联撤回一并失败）。
func failNotifDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:notif_delete", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Notification); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failUserUpdates 让用户行更新失败（通知偏好回写）。
func failUserUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:user_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.User); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

// setWarnPercent 直接写库改阈值（PUT 面校验 1-100，历史坏行只能从库侧造）。
func setWarnPercent(t *testing.T, db *gorm.DB, uid uint, v int) {
	t.Helper()
	if err := db.Model(&storage.User{}).Where("id = ?", uid).
		Update("traffic_warn_percent", v).Error; err != nil {
		t.Fatalf("set warn percent: %v", err)
	}
}

// TestCreateAnnouncementErrors 发布公告的校验与扇出失败口径。
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

	// 扇出库故障：内部错误不外泄细节。
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	rec := doJSON(t, r, http.MethodPost, "/api/notifications/announcement",
		map[string]any{"title": "标"})
	if rec.Code != http.StatusInternalServerError ||
		!strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body)
	}
}

// TestListAnnouncementsErrors 公告列表 limit 校验与三次查询各自失败。
func TestListAnnouncementsErrors(t *testing.T) {
	// limit 越界（缺省 50 不越界，合法值走夹取分支）。
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

	// 锚点列表查询失败。
	r, db := newTestRouterWithDB(t)
	panelToken(t, r, "anon-a")
	notifAnchor(t, db, notifUserID(t, db, "anon-a"))
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/notifications/announcements", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("anchors: %d %s", rec.Code, rec.Body)
	}

	// 发送数统计失败（第 2 次查询）。
	r, db = newTestRouterWithDB(t)
	panelToken(t, r, "anon-b")
	notifAnchor(t, db, notifUserID(t, db, "anon-b"))
	failNotifQueryAt(t, db, 2)
	if rec := doJSON(t, r, http.MethodGet, "/api/notifications/announcements", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("sent count: %d %s", rec.Code, rec.Body)
	}

	// 已读数统计失败（第 3 次查询）。
	r, db = newTestRouterWithDB(t)
	_ = panelToken(t, r, "anon-c")
	notifAnchor(t, db, notifUserID(t, db, "anon-c"))
	failNotifQueryAt(t, db, 3)
	if rec := doJSON(t, r, http.MethodGet, "/api/notifications/announcements", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("read count: %d %s", rec.Code, rec.Body)
	}
}

// TestResendAnnouncementErrors 重发的 id 校验与非冲突类库故障。
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

// TestListNotificationsErrors 全量列表 limit/过滤口径与查询失败。
func TestListNotificationsErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, limit := range []string{"abc", "0", "501"} {
		rec := doJSON(t, r, http.MethodGet, "/api/notifications?limit="+limit, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "1-500") {
			t.Fatalf("limit=%s: code=%d body=%s", limit, rec.Code, rec.Body)
		}
	}

	// 合法 limit 与 user_id 过滤。
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

	// 查询失败。
	r, db = newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/notifications", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteNotificationErrors 删除的 id 校验、定位失败与事务内删除失败。
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

	// 事务内删除失败：定位成功但级联撤回报错。
	r, db = newTestRouterWithDB(t)
	panelToken(t, r, "del-u")
	notifAnchor(t, db, notifUserID(t, db, "del-u"))
	failNotifDeletes(t, db)
	rec = doJSON(t, r, http.MethodDelete, "/api/notifications/1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelNotificationErrors 面板侧未鉴权 404、limit 校验、查询与更新失败。
func TestPanelNotificationErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// 无令牌：六个读/写端点一律 not found。
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

	// limit 越界与合法夹取；列表与未读数查询失败。
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

	// 标已读：id 非数字 400；更新失败 500。
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

	// 全部已读：更新失败。
	r, db = newTestRouterWithDB(t)
	tok = panelToken(t, r, "pnl-all")
	failNotifUpdates(t, db)
	if rec := doPanel(t, r, tok, http.MethodPost, "/api/panel/notifications/read-all", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("read-all: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelNotifyPrefsErrors 偏好：缺字段、更新失败与历史坏行阈值回落。
func TestPanelNotifyPrefsErrors(t *testing.T) {
	// 缺字段 400（无可更新字段）；双布尔更新走通（notify_traffic 字段分支）。
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

	// 坏绑定 400。
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/panel/notify-prefs", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", w.Code, w.Body)
	}

	// 历史坏行阈值回落 80（库侧写入越界值，API 校验拦不住的存量数据）。
	// 须在注错前做：注入的更新失败会挡掉这一步的直接写库。
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

	// 更新失败 500。
	failUserUpdates(t, db)
	rec = doPanel(t, r, tok, http.MethodPut, "/api/panel/notify-prefs",
		map[string]any{"notify_expiry": false})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
}
