package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// auditBodyMax 是请求体快照的存储上限（AU-1）：超长按上限截断，防大请求
// 把审计表冲成日志表。
const auditBodyMax = 2048

// auditRedactKeys 是请求体快照的脱敏键名（AU-1）：密码/令牌/密钥类字段
// 一律回 ***，审计行不携机密明文。
var auditRedactKeys = map[string]bool{
	"password": true, "password_hash": true, "token": true, "api_token": true,
	"secret": true, "secret_key": true, "access_key": true, "authorization": true,
	"totp": true, "recovery_code": true, "sub_token": true,
}

// auditActor 从鉴权面上下文取操作者（AU-1）：apiAuth 双凭据都落
// adminUserID（管理员 JWT）或 apiUser（API Token），distAuth 面不挂本中间件。
func (h *Handler) auditActor(c *gin.Context) (string, string) {
	if id, ok := c.Get("adminUserID"); ok && id != "" {
		// 管理员 JWT：apiUser=Subject（用户名），比数字 ID 可读。
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

// auditBodySnapshot 读请求体并还原（AU-1）：JSON 体递归脱敏敏感键，
// 非 JSON 体按原文截断；读后必须还原 Body 供后续 ShouldBindJSON 使用。
func (h *Handler) auditBodySnapshot(c *gin.Context) string {
	if c.Request.Body == nil || c.Request.Body == http.NoBody {
		return ""
	}
	raw, err := c.GetRawData()
	if err != nil || len(raw) == 0 {
		return ""
	}
	if c.Request.GetBody != nil {
		c.Request.Body, _ = c.Request.GetBody()
	} else {
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return truncate(string(raw), auditBodyMax)
	}
	b, _ := json.Marshal(redactJSON(v))
	return truncate(string(b), auditBodyMax)
}

// redactJSON 递归脱敏 map 的敏感键（AU-1），数组与标量原样保留。
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// auditMiddleware 捕获管理员写操作（AU-1）：POST/PUT/PATCH/DELETE 过
// apiAuth 与 adminAuthMiddleware 的 /api、/admin 组路由统一落 audit_logs。
// GET/HEAD 不捕获（读量大，会把审计表冲成日志表）。本中间件挂鉴权之后：
// 401 在 apiAuth 即中止不落行（登录失败另有 login_logs/限速锁），鉴权通过
// 后的业务失败（409/400/500 等）落行 Success=false。
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

// ListAuditLogs 审计流水查询（AU-1）：操作者/方法/对象/时间范围过滤，
// id DESC 上限 200 缺省 50。
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
