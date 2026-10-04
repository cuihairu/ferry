package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/gin-gonic/gin"
)

const nodeSelect = `SELECT id, name, address, port, protocol, config, enabled, token, last_seen, status, created_at, updated_at FROM nodes`

func (h *Handler) listNodes(c *gin.Context) {
	rows, err := h.db.Query(nodeSelect + ` ORDER BY id`)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	out := []model.Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		out = append(out, *n)
	}
	if err := rows.Err(); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) getNode(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	c.JSON(http.StatusOK, n)
}

func (h *Handler) createNode(c *gin.Context) {
	var in model.NodeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateNodeInput(&in, true); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	token, err := randomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	res, err := h.db.Exec(
		`INSERT INTO nodes (name, address, port, protocol, config, enabled, token) VALUES (?,?,?,?,?,?,?)`,
		strings.TrimSpace(in.Name), strings.TrimSpace(in.Address), in.Port, in.Protocol, normalizeConfig(in.Config), enabled, token)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	id, _ := res.LastInsertId()
	n, err := h.findNode(strconv.FormatInt(id, 10))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, n)
}

func (h *Handler) updateNode(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var in model.NodeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	// 未提供的字段保持原值，整体校验后回写。
	if in.Name == "" {
		in.Name = n.Name
	}
	if in.Address == "" {
		in.Address = n.Address
	}
	if in.Port == 0 {
		in.Port = n.Port
	}
	if in.Protocol == "" {
		in.Protocol = n.Protocol
	}
	if in.Config == "" {
		in.Config = n.Config
	}
	if err := validateNodeInput(&in, false); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	enabled := n.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if _, err := h.db.Exec(
		`UPDATE nodes SET name=?, address=?, port=?, protocol=?, config=?, enabled=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		strings.TrimSpace(in.Name), strings.TrimSpace(in.Address), in.Port, in.Protocol, normalizeConfig(in.Config), enabled, n.ID); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	updated, err := h.findNode(strconv.FormatInt(n.ID, 10))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (h *Handler) deleteNode(c *gin.Context) {
	res, err := h.db.Exec(`DELETE FROM nodes WHERE id=?`, c.Param("id"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

func (h *Handler) findNode(id string) (*model.Node, error) {
	row := h.db.QueryRow(nodeSelect+` WHERE id=?`, id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	return n, err
}

type rowScanner interface{ Scan(dest ...any) error }

func scanNode(r rowScanner) (*model.Node, error) {
	var n model.Node
	var enabled int
	var lastSeen sql.NullTime
	if err := r.Scan(&n.ID, &n.Name, &n.Address, &n.Port, &n.Protocol, &n.Config, &enabled, &n.Token, &lastSeen, &n.Status, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return nil, err
	}
	n.Enabled = enabled == 1
	if lastSeen.Valid {
		n.LastSeen = &lastSeen.Time
	}
	return &n, nil
}

// normalizeConfig 保证模板存的是紧凑 JSON 文本；空对象输入回退为 {}。
func normalizeConfig(cfg string) string {
	trimmed := strings.TrimSpace(cfg)
	if trimmed == "" {
		return "{}"
	}
	var v any
	if json.Unmarshal([]byte(trimmed), &v) != nil {
		return trimmed // validateNodeInput 已确保合法，这里仅防御
	}
	compact, err := json.Marshal(v)
	if err != nil {
		return trimmed
	}
	return string(compact)
}

func validateNodeInput(in *model.NodeInput, creating bool) error {
	if creating && strings.TrimSpace(in.Name) == "" {
		return errors.New("name is required")
	}
	if creating && strings.TrimSpace(in.Address) == "" {
		return errors.New("address is required")
	}
	if in.Port < 1 || in.Port > 65535 {
		return errors.New("port must be 1-65535")
	}
	if !model.ValidProtocol(in.Protocol) {
		return errors.New("protocol must be one of vless/vmess/trojan/shadowsocks")
	}
	if strings.TrimSpace(in.Config) != "" && !json.Valid([]byte(in.Config)) {
		return errors.New("config must be valid JSON")
	}
	return nil
}

// fail 输出统一错误结构；内部错误只记日志不外泄细节。
func fail(c *gin.Context, status int, err error) {
	if status >= 500 {
		c.Error(err) // gin 日志记录
		c.JSON(status, gin.H{"error": "internal error"})
		return
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

var errNotFound = errors.New("not found")

func replyFind(c *gin.Context, err error) {
	if errors.Is(err, errNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	fail(c, http.StatusInternalServerError, err)
}
