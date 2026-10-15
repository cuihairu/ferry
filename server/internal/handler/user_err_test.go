package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 二十四批补口：用户 CRUD 各 400/404/500 口径——校验失败（坏绑定/空名/超长/
// 负配额/非法带宽/非法设备数/非法周期）、模板与落库故障、改名冲突与回落
// 再查失败、删用户与重置令牌的两段清理各自失败。
// 留白：RandomToken 的 crypto/rand 错误与 isDup 的并发竞态（唯一约束被
// ensureUsernameFree 前置拦住，正常流程不可达）不硬凑。

// isUserModel 判断语句是否落在用户表上（Find 链 Model 是切片指针）。
func isUserModel(v any) bool {
	switch v.(type) {
	case *storage.User, []storage.User, *[]storage.User:
		return true
	}
	return false
}

// isSubDeviceModel 判断语句是否落在设备记录表上。
func isSubDeviceModel(v any) bool {
	switch v.(type) {
	case *storage.SubDevice, []storage.SubDevice, *[]storage.SubDevice:
		return true
	}
	return false
}

// failUserQueriesAt 第 n 次（1 起）针对用户表的查询失败：改名冲突与回落
// 再查用次序定向。
func failUserQueriesAt(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:user_query_at", func(tx *gorm.DB) {
		if !isUserModel(tx.Statement.Model) {
			return
		}
		seen++
		if seen == n {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failUserCreates 让用户行创建失败。
func failUserCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:user_create", func(tx *gorm.DB) {
		if isUserModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failUserUpdates 让用户行更新失败（偏好回写/改名/补写停用）。
func failUserRowUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:user_update", func(tx *gorm.DB) {
		if isUserModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failUserDeletes 让用户行删除失败。
func failUserDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:user_delete", func(tx *gorm.DB) {
		if isUserModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failSubDeviceDeletes 让设备记录删除失败。
func failSubDeviceDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:subdevice_delete", func(tx *gorm.DB) {
		if isSubDeviceModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// seedUser 经 API 建用户并返回行 id。
func seedUser(t *testing.T, r *gin.Engine, db *gorm.DB, name string) uint {
	t.Helper()
	rec := doJSON(t, r, http.MethodPost, "/api/users",
		map[string]any{"username": name, "quota_bytes": 1 << 30})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user %s: %d %s", name, rec.Code, rec.Body)
	}
	var u storage.User
	if err := db.Where("username = ?", name).First(&u).Error; err != nil {
		t.Fatalf("find user %s: %v", name, err)
	}
	return u.ID
}

// TestUserListGetErrors 列表查询失败与单个读取的 404/500。
func TestUserListGetErrors(t *testing.T) {
	// 列表查询失败。
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/users", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	r, _ = newTestRouterWithDB(t)
	for _, id := range []string{"abc", "9999"} {
		rec := doJSON(t, r, http.MethodGet, "/api/users/"+id, nil)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not found") {
			t.Fatalf("get %s: %d %s", id, rec.Code, rec.Body)
		}
	}
}

// TestCreateUserErrors 创建的校验、模板故障与落库失败口径。
func TestCreateUserErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body any
		msg  string
	}{
		{"bind", "not json", ""},
		{"empty-username", map[string]any{"username": "  "}, "username is required"},
		{"long-username", map[string]any{"username": strings.Repeat("u", 65)}, "username is required"},
		{"neg-quota", map[string]any{"username": "neg-q", "quota_bytes": -1}, "quota_bytes must be"},
		{"bw", map[string]any{"username": "bw-u", "bw_up_mbps": -5}, "bw_up_mbps/bw_down_mbps must be 0-100000"},
		{"device", map[string]any{"username": "dev-u", "device_limit": 2000}, "device_limit must be 0-1000"},
		{"ip", map[string]any{"username": "ip-u", "ip_limit": -1}, "ip_limit must be 0-1000"},
		{"cycle", map[string]any{"username": "cyc-u", "reset_cycle": "year"}, "reset_cycle must be none/day/week/month"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestRouterWithDB(t)
			rec := doJSON(t, r, http.MethodPost, "/api/users", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400: %s", rec.Code, rec.Body)
			}
			if tc.msg != "" && !strings.Contains(rec.Body.String(), tc.msg) {
				t.Fatalf("body = %s, want %q", rec.Body, tc.msg)
			}
		})
	}

	// 模板读取故障（拆设置表）。
	r, db := newTestRouterWithDB(t)
	if err := db.Migrator().DropTable(&storage.Setting{}); err != nil {
		t.Fatalf("drop settings: %v", err)
	}
	rec := doJSON(t, r, http.MethodPost, "/api/users",
		map[string]any{"username": "tpl-u", "use_template": true})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("template: %d %s", rec.Code, rec.Body)
	}

	// 落库创建失败（唯一性预检已过）。
	r, db = newTestRouterWithDB(t)
	failUserCreates(t, db)
	rec = doJSON(t, r, http.MethodPost, "/api/users", map[string]any{"username": "create-fail"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	// 唯一性预检本身故障（500，非冲突）。
	r, db = newTestRouterWithDB(t)
	failUserQueriesAt(t, db, 1)
	rec = doJSON(t, r, http.MethodPost, "/api/users", map[string]any{"username": "chk-fail"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ensureUsernameFree: %d %s", rec.Code, rec.Body)
	}

	// 显式停用补写失败。
	r, db = newTestRouterWithDB(t)
	failUserRowUpdates(t, db)
	rec = doJSON(t, r, http.MethodPost, "/api/users",
		map[string]any{"username": "disabled-u", "enabled": false})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("enabled write-back: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateUserErrors 更新的定位 404、校验、改名冲突与回落再查失败。
func TestUpdateUserErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, id := range []string{"abc", "9999"} {
		rec := doJSON(t, r, http.MethodPut, "/api/users/"+id, map[string]any{"quota_bytes": 1})
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not found") {
			t.Fatalf("update %s: %d %s", id, rec.Code, rec.Body)
		}
	}

	for _, tc := range []struct {
		name string
		body any
		msg  string
	}{
		{"bind", "not json", ""},
		{"long-username", map[string]any{"username": strings.Repeat("u", 65)}, "username is required"},
		{"neg-quota", map[string]any{"quota_bytes": -1}, "quota_bytes must be"},
		{"bw", map[string]any{"bw_up_mbps": 200000}, "bw_up_mbps/bw_down_mbps must be 0-100000"},
		{"device", map[string]any{"device_limit": 2000}, "device_limit must be 0-1000"},
		{"ip", map[string]any{"ip_limit": -1}, "ip_limit must be 0-1000"},
		{"cycle", map[string]any{"reset_cycle": "year"}, "reset_cycle must be none/day/week/month"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db := newTestRouterWithDB(t)
			seedUser(t, r, db, "upd-"+tc.name)
			rec := doJSON(t, r, http.MethodPut, "/api/users/1", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400: %s", rec.Code, rec.Body)
			}
			if tc.msg != "" && !strings.Contains(rec.Body.String(), tc.msg) {
				t.Fatalf("body = %s, want %q", rec.Body, tc.msg)
			}
		})
	}

	// 合法多字段 PATCH：带宽/设备/IP/到期/周期/停用一次带齐，逐分支落库。
	r, db := newTestRouterWithDB(t)
	seedUser(t, r, db, "upd-fields")
	rec := doJSON(t, r, http.MethodPut, "/api/users/1", map[string]any{
		"bw_up_mbps": 10, "bw_down_mbps": 20,
		"device_limit": 5, "ip_limit": 6,
		"expires_at": "2027-01-01T00:00:00Z", "reset_cycle": "month",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("valid fields: %d %s", rec.Code, rec.Body)
	}
	var upd struct {
		BwDownMbps  int    `json:"bw_down_mbps"`
		DeviceLimit int    `json:"device_limit"`
		IPLimit     int    `json:"ip_limit"`
		ResetCycle  string `json:"reset_cycle"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &upd); err != nil {
		t.Fatal(err)
	}
	if upd.BwDownMbps != 20 || upd.DeviceLimit != 5 || upd.IPLimit != 6 || upd.ResetCycle != "month" {
		t.Fatalf("assigned = %+v", upd)
	}

	// 改名冲突 409（他人占用）。
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "rename-a")
	seedUser(t, r, db, "rename-b")
	rec = doJSON(t, r, http.MethodPut, "/api/users/2", map[string]any{"username": "rename-a"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already exists") {
		t.Fatalf("rename conflict: %d %s", rec.Code, rec.Body)
	}

	// 唯一性预检的库故障（第 2 次查询）。
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "chk-a")
	seedUser(t, r, db, "chk-b")
	failUserQueriesAt(t, db, 2)
	rec = doJSON(t, r, http.MethodPut, "/api/users/2", map[string]any{"username": "chk-a"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ensureUsernameFree: %d %s", rec.Code, rec.Body)
	}

	// 更新语句失败 500；清到期分支。
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "upd-quota")
	failUserRowUpdates(t, db)
	rec = doJSON(t, r, http.MethodPut, "/api/users/1", map[string]any{"quota_bytes": 1000})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("updates: %d %s", rec.Code, rec.Body)
	}
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "upd-clear")
	rec = doJSON(t, r, http.MethodPut, "/api/users/1", map[string]any{"clear_expire": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear expire: %d %s", rec.Code, rec.Body)
	}

	// 回落再查失败（第 3 次查询：定位 1 → 更新 → 复查）。
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "upd-refind")
	failUserQueriesAt(t, db, 2)
	rec = doJSON(t, r, http.MethodPut, "/api/users/1", map[string]any{"quota_bytes": 1000})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("re-find: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteUserErrors 删除的定位 404 与两段清理各自失败。
func TestDeleteUserErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, id := range []string{"abc", "9999"} {
		rec := doJSON(t, r, http.MethodDelete, "/api/users/"+id, nil)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not found") {
			t.Fatalf("delete %s: %d %s", id, rec.Code, rec.Body)
		}
	}

	// 设备记录清理失败。
	r, db := newTestRouterWithDB(t)
	seedUser(t, r, db, "del-dev")
	failSubDeviceDeletes(t, db)
	rec := doJSON(t, r, http.MethodDelete, "/api/users/1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("subdevice cleanup: %d %s", rec.Code, rec.Body)
	}

	// 用户行删除失败。
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "del-user")
	failUserDeletes(t, db)
	rec = doJSON(t, r, http.MethodDelete, "/api/users/1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("user delete: %d %s", rec.Code, rec.Body)
	}
}

// TestResetSubTokenErrors 令牌换发的定位 404、换发失败与设备清理失败。
func TestResetSubTokenErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, id := range []string{"abc", "9999"} {
		rec := doJSON(t, r, http.MethodPost, "/api/users/"+id+"/sub-token", nil)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not found") {
			t.Fatalf("reset %s: %d %s", id, rec.Code, rec.Body)
		}
	}

	// 换发写库失败。
	r, db := newTestRouterWithDB(t)
	seedUser(t, r, db, "reset-a")
	failUserRowUpdates(t, db)
	rec := doJSON(t, r, http.MethodPost, "/api/users/1/sub-token", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reset update: %d %s", rec.Code, rec.Body)
	}

	// 设备记录清理失败。
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "reset-b")
	failSubDeviceDeletes(t, db)
	rec = doJSON(t, r, http.MethodPost, "/api/users/1/sub-token", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reset cleanup: %d %s", rec.Code, rec.Body)
	}

	// 成功路径：令牌换新且旧行作废。
	r, db = newTestRouterWithDB(t)
	seedUser(t, r, db, "reset-ok")
	rec = doJSON(t, r, http.MethodPost, "/api/users/1/sub-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset ok: %d %s", rec.Code, rec.Body)
	}
}
