package handler

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 三十一批补口：管理员管理面（P2-2）错误口径——operator 配额助手查无账
// 号与计数失败、列表取数失败、建号坏绑定/空名与超长名/查名库故障/超长密
// 码 bcrypt 拒收/落库失败、更新坏 id/查无/库故障定位/坏绑定/短密码/超长
// 密码/负配额/空负载/回写失败、删除坏 id/查无/库故障定位/非管理员行/
// 落库失败。
// 留白：RandomToken 的 crypto/rand 错误与 isDup 唯一约束竞态（ensureUsernameFree
// 先行，正常流程不可达）不硬凑。
// 修 bug：查无管理员原本经 replyFind 把 gorm.ErrRecordNotFound 当 500 冒出
// （errNotFound 是另一哨兵），改显式判 gorm 记录不存在回 404。

// seedAdminRow 直落一行子管理员，返回其 ID。
func seedAdminRow(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	row := storage.User{Username: name, SubToken: name + "-sub", Enabled: true,
		IsAdmin: true, AdminRole: RoleOperator, MaxUsers: 2}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed admin %s: %v", name, err)
	}
	return row.ID
}

// TestCheckOperatorQuotaErrors 配额助手：查无 operator 与计数失败。
func TestCheckOperatorQuotaErrors(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	if err := h.checkOperatorQuota("ghost"); err == nil ||
		!containsStr(err.Error(), "operator account not found") {
		t.Fatalf("missing operator: %v", err)
	}

	// 计数失败：定位 operator（第 1 查）放行，配额计数（第 2 查）注错。
	seedAdminRow(t, db, "quota-op")
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:user_query_at", func(tx *gorm.DB) {
		if isUserModel(tx.Statement.Model) || isUserModel(tx.Statement.Dest) {
			seen++
			if seen == 2 {
				tx.AddError(errors.New("db down"))
			}
		}
	})
	if err := h.checkOperatorQuota("quota-op"); err == nil {
		t.Fatal("count error: want err")
	}
}

// TestListAdminsError 列表取数失败 500。
func TestListAdminsError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminRow(t, db, "list-op")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/admins", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateAdminErrors 建号：坏绑定、空名/超长名、查名库故障、超长密码
// bcrypt 拒收与落库失败。
func TestCreateAdminErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/admins", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		name string
		body map[string]any
		msg  string
	}{
		{"empty name", map[string]any{"username": "  ", "password": "password123"}, "username is required"},
		{"long name", map[string]any{"username": strings.Repeat("a", 65), "password": "password123"}, "1-64 chars"},
	} {
		if rec := doJSON(t, r, http.MethodPost, "/api/admins", tc.body); rec.Code != http.StatusBadRequest ||
			!containsStr(rec.Body.String(), tc.msg) {
			t.Fatalf("%s: %d %s", tc.name, rec.Code, rec.Body)
		}
	}

	// 查名库故障（ensureUsernameFree 的 Count 落在用户表）。
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:user_count_q", isUserModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/admins",
		map[string]any{"username": "q-fail", "password": "password123"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("name check: %d %s", rec.Code, rec.Body)
	}

	// 超长密码 bcrypt 拒收（>72 字节 bcrypt 报错）。
	r, db = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/admins",
		map[string]any{"username": "long-pw", "password": strings.Repeat("p", 80)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("long password: %d %s", rec.Code, rec.Body)
	}

	// 落库失败。
	r, db = newTestRouterWithDB(t)
	failCreateOn(t, db, "test:user_create", isUserModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/admins",
		map[string]any{"username": "c-fail", "password": "password123"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateAdminErrors 更新：坏 id、查无 404、库故障定位 500、坏绑定、
// 短密码/超长密码、负配额、空负载与回写失败。
func TestUpdateAdminErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/abc",
		map[string]any{"enabled": false}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// 查无：非未找到类之外的库故障同样落 500 口径（下方 closeSQL）。
	r, _ = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/9999",
		map[string]any{"enabled": false}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedAdminRow(t, db, "upd-op")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1",
		map[string]any{"enabled": false}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("locate: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedAdminRow(t, db, "bind-op")
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1",
		map[string]any{"password": "short"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("short pw: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1",
		map[string]any{"password": strings.Repeat("p", 80)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("long pw: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1",
		map[string]any{"max_users": -1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("neg quota: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: %d %s", rec.Code, rec.Body)
	}

	// 回写失败。
	r, db = newTestRouterWithDB(t)
	seedAdminRow(t, db, "wb-op")
	failUpdateOn(t, db, "test:user_update", isUserModel)
	if rec := doJSON(t, r, http.MethodPut, "/api/admins/1",
		map[string]any{"enabled": false}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("update fail: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteAdminErrors 删除（降级）：坏 id、查无 404、库故障定位 500、
// 非管理员行 404 与降级落库失败。
func TestDeleteAdminErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/admins/abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, _ = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/admins/9999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedAdminRow(t, db, "del-op")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodDelete, "/api/admins/1", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("locate: %d %s", rec.Code, rec.Body)
	}

	// 非管理员行：404（不暴露该 id 存在与否）。
	r, db = newTestRouterWithDB(t)
	plain := storage.User{Username: "plain-user", SubToken: "plain-sub", Enabled: true}
	if err := db.Create(&plain).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, http.MethodDelete, "/api/admins/1", nil); rec.Code != http.StatusNotFound ||
		!containsStr(rec.Body.String(), "not an admin") {
		t.Fatalf("not admin: %d %s", rec.Code, rec.Body)
	}

	// 降级落库失败。
	r, db = newTestRouterWithDB(t)
	seedAdminRow(t, db, "demote-op")
	failUpdateOn(t, db, "test:user_demote", isUserModel)
	if rec := doJSON(t, r, http.MethodDelete, "/api/admins/1", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("demote fail: %d %s", rec.Code, rec.Body)
	}
}
