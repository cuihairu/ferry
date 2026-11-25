package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/safestr"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// auditBodyMax is the storage cap for request body snapshots (AU-1): oversized
// bodies are truncated to the cap, preventing large requests from flooding the
// audit table into a log table.
const auditBodyMax = 2048

// auditRedactKeys holds the key names redacted from request body snapshots
// (AU-1): password/token/secret-style fields always come back as ***, so audit
// rows carry no secret plaintext.
var auditRedactKeys = map[string]bool{
	"password": true, "password_hash": true, "token": true, "api_token": true,
	"secret": true, "secret_key": true, "access_key": true, "authorization": true,
	"totp": true, "recovery_code": true, "sub_token": true,
}

// auditActor picks the operator out of the auth-plane context (AU-1): both
// apiAuth credentials leave adminUserID (admin JWT) or apiUser (API Token)
// behind; the distAuth plane does not carry this middleware.
func (h *Handler) auditActor(c *gin.Context) (string, string) {
	if id, ok := c.Get("adminUserID"); ok && id != "" {
		// Admin JWT: apiUser=Subject (the username), more readable than a numeric ID.
		if sub, ok2 := c.Get("apiUser"); ok2 {
			if s, ok3 := sub.(string); ok3 && s != "" {
				return s, "admin"
			}
		}
		if s, ok2 := id.(string); ok2 {
			return s, "admin"
		}
	}
	if u, ok := c.Get("apiUser"); ok {
		if s, ok2 := u.(string); ok2 && s != "" {
			return s, "api"
		}
	}
	return "unknown", "unknown"
}

// auditBodyReadMax is the read cap for request body snapshots (codex deep
// review P2: reading the whole body pulls every large request into memory and
// amplifies it; auditing only needs a 2048-byte snapshot, so reads are capped
// at 64KB. Over-limit requests get a marker instead of a stored snapshot --
// the truncated prefix would be un-redacted raw text, which must not be
// stored).
const auditBodyReadMax = 64 << 10

// auditBodySnapshot reads the request body and restores it (AU-1): JSON bodies
// get sensitive keys recursively redacted, non-JSON bodies are truncated as-is;
// afterwards the Body must be restored for the later ShouldBindJSON (after a
// capped read, the already-read prefix is concatenated back onto the remaining
// stream, so downstream still receives the full request body).
func (h *Handler) auditBodySnapshot(c *gin.Context) string {
	if c.Request.Body == nil || c.Request.Body == http.NoBody {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, auditBodyReadMax+1))
	if c.Request.GetBody != nil {
		c.Request.Body, _ = c.Request.GetBody()
	} else {
		c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(raw), c.Request.Body))
	}
	if err != nil || len(raw) == 0 {
		return ""
	}
	if len(raw) > auditBodyReadMax {
		return "(请求体超过审计快照读取上限，未入库)"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return truncate(string(raw), auditBodyMax)
	}
	b, _ := json.Marshal(redactJSON(v))
	return truncate(string(b), auditBodyMax)
}

// redactJSON recursively redacts a map's sensitive keys (AU-1); arrays and scalars pass through unchanged.
func redactJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if auditRedactKeys[strings.ToLower(k)] {
				out[k] = "***"
				continue
			}
			out[k] = redactJSON(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactJSON(val)
		}
		return out
	default:
		return v
	}
}

// truncate takes the prefix on a UTF-8 character boundary (snapshot bodies and
// receipt details can contain Chinese, and a hard byte cut would slice a
// multi-byte character in half; the implementation funnels into safestr.Head --
// for invalid UTF-8 input it backs off at most 3 bytes to cut losses and never
// empties the snapshot into an empty string).
func truncate(s string, n int) string {
	return safestr.Head(s, n)
}

// auditMiddleware captures admin write operations (AU-1): POST/PUT/PATCH/
// DELETE routes under /api and /admin passing apiAuth and adminAuthMiddleware
// uniformly land in audit_logs. GET/HEAD are not captured (their read volume
// would flood the audit table into a log table). This middleware is mounted
// after auth: a 401 aborts inside apiAuth and stores no row (login failures
// are covered by login_logs / rate-limit lockout), while business failures
// after auth passes (409/400/500 etc.) store a row with Success=false.
func (h *Handler) auditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
			c.Next()
			return
		}
		actor, actorKind := h.auditActor(c)
		target := c.Param("id")
		body := h.auditBodySnapshot(c)
		c.Next()
		row := storage.AuditLog{
			Actor:     actor,
			ActorKind: actorKind,
			Action:    c.Request.Method + " " + c.Request.URL.Path,
			Target:    target,
			Body:      body,
			Success:   c.Writer.Status() < 400,
			IP:        c.ClientIP(),
		}
		h.db.Create(&row)
	}
}

// auditSensitive lands an audit row for a sensitive operation running outside
// the apiAuth+auditMiddleware plane (F-0045): the distributor and panel
// surfaces carry their own credentials, so their mutating endpoints log
// explicitly instead of relying on the middleware. A write failure only logs
// and never blocks the operation.
func (h *Handler) auditSensitive(actor, actorKind, action, target string, success bool, ip string) {
	row := storage.AuditLog{
		Actor: actor, ActorKind: actorKind, Action: action,
		Target: target, Success: success, IP: ip,
	}
	if err := h.db.Create(&row).Error; err != nil {
		log.Printf("audit sensitive row: %v", err)
	}
}

// ListAuditLogs queries the audit trail (AU-1): filters by actor/method/target/
// time range, id DESC, capped at 200, defaulting to 50.
func (h *Handler) ListAuditLogs(c *gin.Context) {
	limit := 50
	if s := c.Query("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	q := h.db.Order("id DESC").Limit(limit)
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			q = q.Offset(n)
		}
	}
	if v := strings.TrimSpace(c.Query("actor")); v != "" {
		q = q.Where("actor = ?", v)
	}
	if v := strings.TrimSpace(c.Query("method")); v != "" {
		q = q.Where("action LIKE ?", v+" %")
	}
	if v := strings.TrimSpace(c.Query("target")); v != "" {
		q = q.Where("target = ?", v)
	}
	if v := strings.TrimSpace(c.Query("from")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("created_at >= ?", t)
		}
	}
	if v := strings.TrimSpace(c.Query("to")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q = q.Where("created_at <= ?", t)
		}
	}
	var rows []storage.AuditLog
	if err := q.Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"audit_logs": rows})
}
