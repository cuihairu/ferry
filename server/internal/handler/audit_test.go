package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
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
