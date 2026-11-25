package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AU-1 (P2-6 watch item promoted to a formal requirement): admin write
// operations uniformly land in audit_logs -- who/when/action/target/request
// body snapshot (redacted)/outcome/IP, pageable from the dash; GET is not
// captured.

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
	// GET is not captured.
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
	// Read back through the query endpoint.
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
	seedAdminForJWT(t, db)
	// A business failure (409 duplicate username) also stores a row.
	doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "au-dup", "password": "pw-123"})
	doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "au-dup", "password": "pw-123"})
	var rows []storage.AuditLog
	db.Order("id").Find(&rows)
	if len(rows) != 2 || rows[1].Success {
		t.Fatalf("rows = %d, second success = %v, want 2/false", len(rows), rows[1].Success)
	}
	// actor filter.
	rec := doJSON(t, r, "GET", "/api/audit-logs?actor=nobody", nil)
	var out struct {
		AuditLogs []storage.AuditLog `json:"audit_logs"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.AuditLogs) != 0 {
		t.Fatalf("actor filter = %d rows, want 0", len(out.AuditLogs))
	}
	// method filter (no DELETE rows).
	rec = doJSON(t, r, "GET", "/api/audit-logs?method=DELETE", nil)
	json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.AuditLogs) != 0 {
		t.Fatalf("method filter = %d rows, want 0", len(out.AuditLogs))
	}
	// The admin group follows the same rule: write operations store rows (2FA state toggle).
	doJSON(t, r, "POST", "/admin/2fa/setup", nil)
	if n := auditCount(t, db); n != 3 {
		t.Fatalf("audit rows after admin write = %d, want 3", n)
	}
}

func TestAuditLogsUnauthenticatedWriteNotCaptured(t *testing.T) {
	// A 401 aborts inside apiAuth, so the audit middleware stores no row (login
	// failures are covered by login_logs instead).
	r, db := newTestRouterWithDB(t)
	req401(t, r, "POST", "/api/users", "Bearer garbage-token-xyz")
	if n := auditCount(t, db); n != 0 {
		t.Fatalf("audit rows after 401 = %d, want 0", n)
	}
}

// TestAuditListGuards covers the audit query guards: limit clamping to 1..200,
// an invalid offset skipped / a valid offset applied, target filtering, invalid
// from/to times skipped and valid times filtered, and a fetch failure as 500.
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
	// Limit clamping: 0 -> 1, 500 -> 200 (the table only holds 3 rows).
	if n := count("?limit=0"); n != 1 {
		t.Fatalf("limit=0 → %d rows, want 1", n)
	}
	if n := count("?limit=500"); n != 3 {
		t.Fatalf("limit=500 → %d rows, want 3", n)
	}
	// An invalid offset is skipped: the newest row is still fetched.
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
	// offset=0 is likewise skipped; offset=1 applies and skips the newest row.
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
	// Target filter: PUT and DELETE both carry target=1.
	if n := count("?target=1"); n != 2 {
		t.Fatalf("target=1 → %d rows, want 2", n)
	}
	// Invalid from/to times are skipped -> the full set.
	if n := count("?from=abc"); n != 3 {
		t.Fatalf("from=abc → %d rows, want 3", n)
	}
	if n := count("?to=abc"); n != 3 {
		t.Fatalf("to=abc → %d rows, want 3", n)
	}
	// Valid times: a from earlier than everything filters nothing; a to earlier than everything -> 0 rows.
	if n := count("?from=2000-01-01T00:00:00Z"); n != 3 {
		t.Fatalf("early from → %d rows, want 3", n)
	}
	if n := count("?to=2000-01-01T00:00:00Z"); n != 0 {
		t.Fatalf("early to → %d rows, want 0", n)
	}

	// A fetch failure -> 500.
	db.Callback().Query().Before("gorm:query").Register("t:auditfail", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*[]storage.AuditLog); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, "GET", "/api/audit-logs", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("query fail: %d %s", rec.Code, rec.Body)
	}
}

// TestAuditActorAndSnapshot tests auditActor's api/unknown branches and
// auditBodySnapshot's GetBody restore branch directly (httptest requests carry
// no GetBody, so it is only reachable via a request built with http.NewRequest).
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

	// The GetBody branch: the body is restored after reading, so later handling
	// still sees the original text.
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

// TestTruncateUTF8Boundary covers the P2 "UTF-8 truncation" fix: a byte cut
// backs off to a UTF-8 character boundary -- an over-long snapshot containing
// Chinese is still valid UTF-8 after truncation; when the boundary lands
// exactly at a character start the cut runs the full cap in bytes; short text
// passes through.
func TestTruncateUTF8Boundary(t *testing.T) {
	// The cap byte lands mid multi-byte character (the character below starts at byte 253): back off to 253.
	s := strings.Repeat("x", 253) + strings.Repeat("流", 10)
	if got := truncate(s, 255); len(got) != 253 || !utf8.ValidString(got) {
		t.Fatalf("mid-rune truncate = %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	// The boundary lands exactly at a character start (85x3=255): cut the full cap in bytes.
	if got := truncate(strings.Repeat("流", 100), 255); len(got) != 255 || !utf8.ValidString(got) {
		t.Fatalf("aligned truncate = %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	if got := truncate("short", 255); got != "short" {
		t.Fatalf("passthrough = %q", got)
	}
}
