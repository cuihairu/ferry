package handler

// Bulk user import/export (user migration surface) tests: export returns all
// users with a count and without the password field; import creates accounts
// (including disabled rows and expiry times), duplicate names skipped and
// reported, bad row 400s the whole batch with zero writes, empty payload and
// over-cap row counts 400.

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

// TestUserExportImport covers full export and import account creation / duplicate skip.
func TestUserExportImport(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	panelToken(t, r, "mig-a")
	panelToken(t, r, "mig-b")

	// Export: all users + count, subscription tokens included, password field excluded.
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

	// Import: 2 valid rows (one disabled, one with expiry) -> 201, accounts created with fresh tokens.
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

	// Duplicate name skip: mig-a already exists -> skipped and reported, mig-e still created.
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

// TestUserImportValidation covers bad-row whole-batch 400 with zero writes, empty payload, and the row cap.
func TestUserImportValidation(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	before := func() int64 {
		var n int64
		if err := db.Model(&storage.User{}).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Bad row (empty username) + good row -> 400, the good row never lands either.
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

	// Empty payload 400.
	if rec := doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": []map[string]any{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty: %d, want 400", rec.Code)
	}
	// Over the row cap 400.
	rows := make([]map[string]any, 501)
	for i := range rows {
		rows[i] = map[string]any{"username": fmt.Sprintf("bulk-%d", i)}
	}
	if rec := doJSON(t, r, "POST", "/api/users/import", map[string]any{"users": rows}); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize: %d, want 400", rec.Code)
	}
	// Bad payload 400.
	if rec := doRaw(t, r, "POST", "/api/users/import", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d, want 400", rec.Code)
	}
	// Row-level validation negatives: overlong token / negative quota / invalid
	// bandwidth / invalid device count, whole batch 400 with zero writes.
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

	// Template-based import: template values fill the row's absent fields.
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

// TestUserImportPreserveToken covers importing with a reused old token
// (address-preserving migration), occupied tokens and in-batch duplicates 400ing
// the whole batch with zero writes, and a fresh token still minted when none is passed.
func TestUserImportPreserveToken(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	panelToken(t, r, "tok-old")
	var old storage.User
	if err := db.Where("username = ?", "tok-old").First(&old).Error; err != nil {
		t.Fatal(err)
	}

	// Account created with the old token: persisted value matches the input (subscription address unchanged).
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

	// Occupied token (already used by someone else) -> whole batch 400 with zero writes.
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

	// In-batch duplicate token -> 400 with zero writes.
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

	// Without sub_token a fresh token is still minted (regression of the original terms).
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

// doJSONToken matches doJSON's terms but sends the given token (for operator-tier quota cases).
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

// TestUserImportDBErrors covers db failure branches at each export/import step:
// export fetch, template load, reused-token dedup check, sub-admin quota count
// and missing account 403ing the whole batch (operator token terms), and
// in-transaction duplicate-count / account creation / disable-write failures
// with rollback and zero writes.
func TestUserImportDBErrors(t *testing.T) {
	// Export fetch failure -> 500.
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, "GET", "/api/users/export", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}

	// Template load failure during a template-based import -> 500.
	r2, db2 := newTestRouterWithDB(t)
	closeSQL(t, db2)
	if rec := doJSON(t, r2, "POST", "/api/users/import", map[string]any{
		"users": []map[string]any{{"username": "tpl-err", "use_template": true}},
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("template load: %d %s", rec.Code, rec.Body)
	}

	// Reused-token dedup failure -> 500 (db-side errors go 500 with an empty username, not a 400 rejection).
	r3, db3 := newTestRouterWithDB(t)
	closeSQL(t, db3)
	if rec := doJSON(t, r3, "POST", "/api/users/import", map[string]any{
		"users": []map[string]any{{"username": "tk-err", "sub_token": "tk-1"}},
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("token check: %d %s", rec.Code, rec.Body)
	}

	// Operator with no matching admin account -> whole batch 403 with zero writes.
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

	// Operator quota count failure -> 403 (count query injected with an error, the lookup query passes first).
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

	// In-transaction duplicate-count failure -> 500.
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

	// In-transaction account creation failure -> 500 (rollback, zero writes).
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

	// Disable write-back failure -> 500 (the explicit enabled:false write-back
	// branch, rollback with zero writes).
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
