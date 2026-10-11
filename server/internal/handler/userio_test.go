package handler

// 用户批量导入/导出（用户迁移面）测试：导出全量带 count 且不带口令字段；
// 导入建号（含禁用行与到期时间）、重名跳过回报、坏行整批 400 零写入、
// 空载荷与超行数上限 400。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
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
	if n := before(); n != 0 {
		t.Fatalf("rejected imports wrote %d users, want 0", n)
	}
}
