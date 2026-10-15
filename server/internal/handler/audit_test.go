package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AU-1（P2-6 观察项转正）：管理员写操作统一落 audit_logs——谁/何时/动作/
// 对象/请求体快照（脱敏）/成败/IP，dash 分页可查；GET 不捕获。

func auditCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&storage.AuditLog{}).Count(&n).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

func TestAuditLogsCaptureWriteOps(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	doJSON(t, r, "POST", "/api/users", map[string]any{"username": "au-user", "password": "pass-123"})
	doJSON(t, r, "PUT", "/api/users/1", map[string]any{"note": "x"})
	doJSON(t, r, "DELETE", "/api/users/1", nil)
	if n := auditCount(t, db); n != 3 {
		t.Fatalf("audit rows after 3 writes = %d, want 3", n)
	}
	// GET 不捕获。
	doJSON(t, r, "GET", "/api/users", nil)
	if n := auditCount(t, db); n != 3 {
		t.Fatalf("audit rows after GET = %d, want 3", n)
	}
	var rows []storage.AuditLog
	db.Order("id").Find(&rows)
	if rows[0].Action != "POST /api/users" || rows[0].ActorKind != "admin" {
		t.Fatalf("row0 = %s/%s, want POST /api/users/admin", rows[0].Action, rows[0].ActorKind)
	}
	if !rows[0].Success || !rows[2].Success {
		t.Fatalf("success flags = %v/%v, want true/true", rows[0].Success, rows[2].Success)
	}
	if rows[2].Target != "1" {
		t.Fatalf("delete target = %q, want 1", rows[2].Target)
	}
	// 查询端点回读。
	rec := doJSON(t, r, "GET", "/api/audit-logs?limit=2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/audit-logs = %d %s", rec.Code, rec.Body)
	}
	var out struct {
		AuditLogs []storage.AuditLog `json:"audit_logs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.AuditLogs) != 2 || out.AuditLogs[0].Action != "DELETE /api/users/1" {
		t.Fatalf("query rows = %d first=%s, want 2 DELETE first", len(out.AuditLogs), out.AuditLogs[0].Action)
	}
}

func TestAuditLogsRedactSensitiveBody(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	doJSON(t, r, "POST", "/api/distributors", map[string]any{
		"username": "au-dist", "password": "super-secret-pw", "discount_percent": 30,
	})
	var row storage.AuditLog
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("first audit: %v", err)
	}
	if strings.Contains(row.Body, "super-secret-pw") {
		t.Fatalf("password leaked into audit body: %s", row.Body)
	}
	if !strings.Contains(row.Body, `"password":"***"`) || !strings.Contains(row.Body, "au-dist") {
		t.Fatalf("redacted body = %s", row.Body)
	}
}

func TestAuditLogsCaptureFailedWriteAndFilters(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	// 业务失败（409 重复用户名）也落行。
	doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "au-dup", "password": "pw-123"})
	doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "au-dup", "password": "pw-123"})
	var rows []storage.AuditLog
	db.Order("id").Find(&rows)
	if len(rows) != 2 || rows[1].Success {
		t.Fatalf("rows = %d, second success = %v, want 2/false", len(rows), rows[1].Success)
	}
	// actor 过滤。
	rec := doJSON(t, r, "GET", "/api/audit-logs?actor=nobody", nil)
	var out struct {
		AuditLogs []storage.AuditLog `json:"audit_logs"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.AuditLogs) != 0 {
		t.Fatalf("actor filter = %d rows, want 0", len(out.AuditLogs))
	}
	// method 过滤（DELETE 无行）。
	rec = doJSON(t, r, "GET", "/api/audit-logs?method=DELETE", nil)
	json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.AuditLogs) != 0 {
		t.Fatalf("method filter = %d rows, want 0", len(out.AuditLogs))
	}
	// admin 组同口径：写操作落行（2FA 状态切换）。
	doJSON(t, r, "POST", "/admin/2fa/setup", nil)
	if n := auditCount(t, db); n != 3 {
		t.Fatalf("audit rows after admin write = %d, want 3", n)
	}
}

func TestAuditLogsUnauthenticatedWriteNotCaptured(t *testing.T) {
	// 401 在 apiAuth 中止，审计中间件不落行（登录失败面另有 login_logs）。
	r, db := newTestRouterWithDB(t)
	req401(t, r, "POST", "/api/users", "Bearer garbage-token-xyz")
	if n := auditCount(t, db); n != 0 {
		t.Fatalf("audit rows after 401 = %d, want 0", n)
	}
}

// TestAuditListGuards 覆盖审计查询护栏：limit 夹取 1..200、offset 非法跳过/
// 合法生效、target 过滤、from/to 非法时间跳过与有效时间过滤、取数失败 500。
func TestAuditListGuards(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	doJSON(t, r, "POST", "/api/users", map[string]any{"username": "ag-1", "password": "pw-12345"})
	doJSON(t, r, "PUT", "/api/users/1", map[string]any{"note": "x"})
	doJSON(t, r, "DELETE", "/api/users/1", nil)
	if n := auditCount(t, db); n != 3 {
		t.Fatalf("seed rows = %d, want 3", n)
	}
	count := func(query string) int {
		t.Helper()
		rec := doJSON(t, r, "GET", "/api/audit-logs"+query, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", query, rec.Code, rec.Body)
		}
		var out struct {
			AuditLogs []storage.AuditLog `json:"audit_logs"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return len(out.AuditLogs)
	}
	// limit 夹取：0→1，500→200（表里只有 3 行）。
	if n := count("?limit=0"); n != 1 {
		t.Fatalf("limit=0 → %d rows, want 1", n)
	}
	if n := count("?limit=500"); n != 3 {
		t.Fatalf("limit=500 → %d rows, want 3", n)
	}
	// offset 非法跳过：仍取最新一行。
	if n := count("?limit=1&offset=abc"); n != 1 {
		t.Fatalf("offset=abc → %d rows, want 1", n)
	}
	rec := doJSON(t, r, "GET", "/api/audit-logs?limit=1&offset=abc", nil)
	var out struct {
		AuditLogs []storage.AuditLog `json:"audit_logs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.AuditLogs[0].Action != "DELETE /api/users/1" {
		t.Fatalf("bad offset should return newest: %s", out.AuditLogs[0].Action)
	}
	// offset=0 同跳过；offset=1 生效跳过最新一行。
	if n := count("?limit=2&offset=0"); n != 2 {
		t.Fatalf("offset=0 → %d rows, want 2", n)
	}
	rec = doJSON(t, r, "GET", "/api/audit-logs?limit=2&offset=1", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.AuditLogs[0].Action != "PUT /api/users/1" {
		t.Fatalf("offset=1 should skip newest: %s", out.AuditLogs[0].Action)
	}
	// target 过滤：PUT 与 DELETE 都带 target=1。
	if n := count("?target=1"); n != 2 {
		t.Fatalf("target=1 → %d rows, want 2", n)
	}
	// from/to 非法时间跳过 → 全量。
	if n := count("?from=abc"); n != 3 {
		t.Fatalf("from=abc → %d rows, want 3", n)
	}
	if n := count("?to=abc"); n != 3 {
		t.Fatalf("to=abc → %d rows, want 3", n)
	}
	// 有效时间：from 早于全部不筛；to 早于全部 → 0 行。
	if n := count("?from=2000-01-01T00:00:00Z"); n != 3 {
		t.Fatalf("early from → %d rows, want 3", n)
	}
	if n := count("?to=2000-01-01T00:00:00Z"); n != 0 {
		t.Fatalf("early to → %d rows, want 0", n)
	}

	// 取数失败 → 500。
	db.Callback().Query().Before("gorm:query").Register("t:auditfail", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*[]storage.AuditLog); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, "GET", "/api/audit-logs", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("query fail: %d %s", rec.Code, rec.Body)
	}
}

// TestAuditActorAndSnapshot 直测 auditActor 的 api/unknown 分支与
// auditBodySnapshot 的 GetBody 还原分支（httptest 请求不带 GetBody，
// 需 http.NewRequest 构造才走得到）。
func TestAuditActorAndSnapshot(t *testing.T) {
	h := &Handler{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("apiUser", "bob")
	if a, k := h.auditActor(c); a != "bob" || k != "api" {
		t.Fatalf("api actor = %s/%s, want bob/api", a, k)
	}
	empty, _ := gin.CreateTestContext(httptest.NewRecorder())
	if a, k := h.auditActor(empty); a != "unknown" || k != "unknown" {
		t.Fatalf("unknown actor = %s/%s", a, k)
	}

	// GetBody 分支：读后还原，后续处理仍能拿到原文。
	req, err := http.NewRequest("POST", "/x", strings.NewReader(`{"password":"secret-z","a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := gin.CreateTestContext(httptest.NewRecorder())
	cs.Request = req
	snap := h.auditBodySnapshot(cs)
	if !strings.Contains(snap, `"password":"***"`) || strings.Contains(snap, "secret-z") {
		t.Fatalf("snapshot = %s", snap)
	}
	rest, err := io.ReadAll(cs.Request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != `{"password":"secret-z","a":1}` {
		t.Fatalf("body not restored: %q", string(rest))
	}
}
