package handler

// 用户批量导入/导出（用户迁移面）测试：导出全量带 count 且不带口令字段；
// 导入建号（含禁用行与到期时间）、重名跳过回报、坏行整批 400 零写入、
// 空载荷与超行数上限 400。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TestUserExportImport 覆盖导出全量与导入建号/重名跳过。
func TestUserExportImport(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	panelToken(t, r, "mig-a")
	panelToken(t, r, "mig-b")

	// 导出：全量用户+count，含订阅令牌、不含口令字段。
	rec := doJSON(t, r, "GET", "/api/users/export", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
	var exp struct {
		Count int            `json:"count"`
		Users []storage.User `json:"users"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &exp)
	if exp.Count != 2 || len(exp.Users) != 2 {
		t.Fatalf("export = count %d users %d, want 2/2", exp.Count, len(exp.Users))
	}
	for _, u := range exp.Users {
		if u.SubToken == "" {
			t.Fatalf("export row %s missing sub_token", u.Username)
		}
	}
	if strings.Contains(rec.Body.String(), `"password"`) {
		t.Fatal("export leaks password field")
	}

	// 导入：2 行有效（一行禁用、一行带到期）→ 201 建号带新令牌。
	exp30 := time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	rec = doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": []map[string]any{
		{"username": "mig-c", "quota_bytes": 1 << 30, "expires_at": exp30},
		{"username": "mig-d", "enabled": false},
	}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Created int            `json:"created"`
		Skipped int            `json:"skipped"`
		Users   []storage.User `json:"users"`
		Rows    []struct {
			Username string `json:"username"`
			Reason   string `json:"reason"`
		} `json:"skipped_rows"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Created != 2 || out.Skipped != 0 || len(out.Users) != 2 {
		t.Fatalf("import resp = %+v", out)
	}
	var c, d storage.User
	if err := db.Where("username = ?", "mig-c").First(&c).Error; err != nil {
		t.Fatal(err)
	}
	if c.QuotaBytes != 1<<30 || c.ExpiresAt == nil || c.SubToken == "" {
		t.Fatalf("mig-c persisted = %+v", c)
	}
	if err := db.Where("username = ?", "mig-d").First(&d).Error; err != nil {
		t.Fatal(err)
	}
	if d.Enabled {
		t.Fatal("mig-d should be disabled")
	}

	// 重名跳过：mig-a 已存在 → 跳过并回报，mig-e 照建。
	rec = doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": []map[string]any{
		{"username": "mig-a"},
		{"username": "mig-e"},
	}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("import dup: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Created != 1 || out.Skipped != 1 || len(out.Rows) != 1 {
		t.Fatalf("dup resp = %+v", out)
	}
	if out.Rows[0].Username != "mig-a" || out.Rows[0].Reason == "" {
		t.Fatalf("dup skip row = %+v", out.Rows[0])
	}
	var total int64
	if err := db.Model(&storage.User{}).Count(&total).Error; err != nil {
		t.Fatal(err)
	}
	if total != 5 { // mig-a/b/c/d/e
		t.Fatalf("users after import = %d, want 5", total)
	}
}

// TestUserImportValidation 覆盖坏行整批 400 零写入、空载荷与超行数上限。
func TestUserImportValidation(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	before := func() int64 {
		var n int64
		if err := db.Model(&storage.User{}).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}

	// 坏行（空用户名）+ 好行 → 400，好行也不落库。
	rec := doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": []map[string]any{
		{"username": ""},
		{"username": "mig-ok"},
	}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad row: %d, want 400", rec.Code)
	}
	var out struct {
		Created int `json:"created"`
		Rows    []struct {
			Username string `json:"username"`
			Reason   string `json:"reason"`
		} `json:"skipped_rows"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Created != 0 || len(out.Rows) != 1 || out.Rows[0].Reason == "" {
		t.Fatalf("bad row resp = %+v", out)
	}
	if n := before(); n != 0 {
		t.Fatalf("bad row wrote %d users, want 0", n)
	}

	// 空载荷 400。
	if rec := doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": []map[string]any{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty: %d, want 400", rec.Code)
	}
	// 超行数上限 400。
	rows := make([]map[string]any, 501)
	for i := range rows {
		rows[i] = map[string]any{"username": fmt.Sprintf("bulk-%d", i)}
	}
	if rec := doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": rows}); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize: %d, want 400", rec.Code)
	}
	// 坏载荷 400。
	if rec := doRaw(t, r, "POST", "/api/users/import", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d, want 400", rec.Code)
	}
	// 行级校验负例：超长令牌/负配额/非法带宽/非法设备数，整批 400 零写入。
	for _, row := range []map[string]any{
		{"username": "v1", "sub_token": strings.Repeat("t", 65)},
		{"username": "v2", "quota_bytes": -1},
		{"username": "v3", "bw_up_mbps": -1},
		{"username": "v4", "device_limit": 1001},
	} {
		if rec := doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": []map[string]any{row}}); rec.Code != http.StatusBadRequest {
			t.Fatalf("row %s: %d %s", row["username"], rec.Code, rec.Body)
		}
	}
	if n := before(); n != 0 {
		t.Fatalf("rejected imports wrote %d users, want 0", n)
	}

	// 套模板导入：模板值补进行内缺省项。
	if rec := doJSON(t, r, "PUT", "/api/user-template", map[string]any{
		"quota_bytes": 1073741824, "expire_days": 30, "reset_cycle": "month",
	}); rec.Code != http.StatusOK {
		t.Fatalf("put template: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": []map[string]any{
		{"username": "tpl-user", "use_template": true},
	}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("template import: %d %s", rec.Code, rec.Body)
	}
	var tplUser storage.User
	if err := db.Where("username = ?", "tpl-user").First(&tplUser).Error; err != nil {
		t.Fatal(err)
	}
	if tplUser.QuotaBytes != 1073741824 || tplUser.ResetCycle != "month" {
		t.Fatalf("template applied = %+v", tplUser)
	}
}

// TestUserImportPreserveToken 覆盖导入沿用旧令牌（迁移保址）、令牌占用与批内
// 重复整批 400 零写入、不传令牌仍生成新令牌。
func TestUserImportPreserveToken(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	panelToken(t, r, "tok-old")
	var old storage.User
	if err := db.Where("username = ?", "tok-old").First(&old).Error; err != nil {
		t.Fatal(err)
	}

	// 沿用旧令牌建号：落库值与传入一致（订阅地址不换）。
	rec := doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": []map[string]any{
		{"username": "tok-new", "sub_token": "legacy-token-value-1"},
	}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	var got storage.User
	if err := db.Where("username = ?", "tok-new").First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.SubToken != "legacy-token-value-1" {
		t.Fatalf("preserved token = %q, want legacy-token-value-1", got.SubToken)
	}

	// 占用令牌（他人已在用）→ 整批 400 零写入。
	rec = doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": []map[string]any{
		{"username": "tok-x", "sub_token": old.SubToken},
	}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("taken token: %d, want 400", rec.Code)
	}
	var cnt int64
	if err := db.Model(&storage.User{}).Where("username = ?", "tok-x").Count(&cnt).Error; err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("taken-token import wrote %d rows, want 0", cnt)
	}

	// 批内重复令牌 → 400 零写入。
	rec = doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": []map[string]any{
		{"username": "tok-y", "sub_token": "dup-token"},
		{"username": "tok-z", "sub_token": "dup-token"},
	}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dup in batch: %d, want 400", rec.Code)
	}
	var dup int64
	if err := db.Model(&storage.User{}).Where("username IN ?", []string{"tok-y", "tok-z"}).Count(&dup).Error; err != nil {
		t.Fatal(err)
	}
	if dup != 0 {
		t.Fatalf("dup-token import wrote %d rows, want 0", dup)
	}

	// 不传 sub_token 仍生成新令牌（原口径回归）。
	rec = doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": []map[string]any{
		{"username": "tok-gen"},
	}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("generated: %d %s", rec.Code, rec.Body)
	}
	var gen storage.User
	if err := db.Where("username = ?", "tok-gen").First(&gen).Error; err != nil {
		t.Fatal(err)
	}
	if gen.SubToken == "" || gen.SubToken == "legacy-token-value-1" {
		t.Fatalf("generated token = %q", gen.SubToken)
	}
}

// doJSONToken 与 doJSON 同口径但用指定令牌（operator 层级配额面用例用）。
func doJSONToken(t *testing.T, r *gin.Engine, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestUserImportDBErrors 覆盖导出/导入各步取数与落库失败分支：导出取数、
// 模板读取、沿用令牌查重、子管理员配额计数与无账号整批 403（operator 令牌
// 口径）、事务内重名计数/建号/停用补写失败且回滚零写入。
func TestUserImportDBErrors(t *testing.T) {
	// 导出取数失败 → 500。
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, "GET", "/api/users/export", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}

	// 套模板导入时模板读取失败 → 500。
	r2, db2 := newTestRouterWithDB(t)
	closeSQL(t, db2)
	if rec := doJSON(t, r2, "POST", "/api/users/import", map[string]any{
		"users": []map[string]any{{"username": "tpl-err", "use_template": true}},
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("template load: %d %s", rec.Code, rec.Body)
	}

	// 沿用令牌查重失败 → 500（db 侧错误按空用户名走 500，不按 400 驳回）。
	r3, db3 := newTestRouterWithDB(t)
	closeSQL(t, db3)
	if rec := doJSON(t, r3, "POST", "/api/users/import", map[string]any{
		"users": []map[string]any{{"username": "tk-err", "sub_token": "tk-1"}},
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("token check: %d %s", rec.Code, rec.Body)
	}

	// operator 无对应管理员账号 → 整批 403 零写入。
	r4, db4 := newTestRouterWithDB(t)
	if rec := doJSONToken(t, r4, opJWT(t, "ghost-op"), "POST", "/api/users/import",
		map[string]any{"users": []map[string]any{{"username": "imp-1"}}}); rec.Code != http.StatusForbidden {
		t.Fatalf("missing operator: %d %s", rec.Code, rec.Body)
	}
	var zero4 int64
	if err := db4.Model(&storage.User{}).Count(&zero4).Error; err != nil {
		t.Fatal(err)
	}
	if zero4 != 0 {
		t.Fatalf("quota reject wrote %d users, want 0", zero4)
	}

	// operator 配额计数失败 → 403（计数查询注错，定位查询先放行）。
	r5, db5 := newTestRouterWithDB(t)
	seedAdminRow(t, db5, "imp-op")
	db5.Callback().Query().Before("gorm:query").Register("t:quotacount", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*int64); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSONToken(t, r5, opJWT(t, "imp-op"), "POST", "/api/users/import",
		map[string]any{"users": []map[string]any{{"username": "imp-2"}}}); rec.Code != http.StatusForbidden {
		t.Fatalf("quota count: %d %s", rec.Code, rec.Body)
	}

	// 事务内重名计数失败 → 500。
	r6, db6 := newTestRouterWithDB(t)
	db6.Callback().Query().Before("gorm:query").Register("t:dupcount", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*int64); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r6, "POST", "/api/users/import", map[string]any{
		"users": []map[string]any{{"username": "imp-3"}},
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("dup count: %d %s", rec.Code, rec.Body)
	}

	// 事务内建号失败 → 500（回滚零写入）。
	r7, db7 := newTestRouterWithDB(t)
	db7.Callback().Create().Before("gorm:create").Register("t:importcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.User); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r7, "POST", "/api/users/import", map[string]any{
		"users": []map[string]any{{"username": "imp-4"}},
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var zero7 int64
	if err := db7.Model(&storage.User{}).Count(&zero7).Error; err != nil {
		t.Fatal(err)
	}
	if zero7 != 0 {
		t.Fatalf("import rollback leaked %d users", zero7)
	}

	// 停用补写失败 → 500（显式 enabled:false 的补写分支，回滚零写入）。
	r8, db8 := newTestRouterWithDB(t)
	db8.Callback().Update().Before("gorm:update").Register("t:disablewrite", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.User); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r8, "POST", "/api/users/import", map[string]any{
		"users": []map[string]any{{"username": "imp-5", "enabled": false}},
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("disable write: %d %s", rec.Code, rec.Body)
	}
	var zero8 int64
	if err := db8.Model(&storage.User{}).Count(&zero8).Error; err != nil {
		t.Fatal(err)
	}
	if zero8 != 0 {
		t.Fatalf("disable write leaked %d users", zero8)
	}
}
