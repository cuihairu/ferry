package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *Handler) listNodes(c *gin.Context) {
	out := []storage.Node{}
	if err := h.db.Order("id").Find(&out).Error; err != nil {
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
	n, err := newPersistedNode(h.db, &in)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, n)
}

// newPersistedNode builds a node from the registration payload and persists
// it: issue the agent token -> metadata initial values -> an explicit
// Enabled=false write-back. Admin-side node creation and the FERRY-001
// credential exchange share this path, so nodes created either way have the
// exact same shape.
func newPersistedNode(db *gorm.DB, in *model.NodeInput) (*storage.Node, error) {
	token, err := RandomToken()
	if err != nil {
		return nil, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	maintenance := false
	if in.Maintenance != nil {
		maintenance = *in.Maintenance
	}
	n := storage.Node{
		Name:        strings.TrimSpace(in.Name),
		Address:     strings.TrimSpace(in.Address),
		Port:        in.Port,
		Protocol:    in.Protocol,
		Config:      normalizeConfig(in.Config),
		Enabled:     enabled,
		Maintenance: maintenance,
		Token:       token,
		Status:      "unknown",
	}
	// metadata: non-empty values override the DB defaults, and any one present
	// sets MetaInit (panel takeover, registration no longer overrides).
	applyMetaInput(&n, in)
	if err := db.Create(&n).Error; err != nil {
		return nil, err
	}
	if !enabled {
		// Enabled carries default:true, so the zero value false would be skipped in
		// favor of the DB default; an explicit write-back is needed.
		if err := db.Model(&storage.Node{}).Where("id=?", n.ID).Update("enabled", false).Error; err != nil {
			return nil, err
		}
		n.Enabled = false
	}
	return &n, nil
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
	// fields left out keep their current values; validate the whole input, then write back.
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
	maintenance := n.Maintenance
	if in.Maintenance != nil {
		maintenance = *in.Maintenance
	}
	updates := map[string]any{
		"name":        strings.TrimSpace(in.Name),
		"address":     strings.TrimSpace(in.Address),
		"port":        in.Port,
		"protocol":    in.Protocol,
		"config":      normalizeConfig(in.Config),
		"enabled":     enabled,
		"maintenance": maintenance,
	}
	// metadata left out keeps its current value (same rule as the base fields);
	// provided values are written back after validation.
	metaProvided := false
	for k, v := range metaUpdates(&in) {
		updates[k] = v
		metaProvided = true
	}
	if metaProvided {
		// once the panel has edited metadata, registration reports no longer override it.
		updates["meta_init"] = true
	}
	if err := h.db.Model(&storage.Node{}).Where("id=?", n.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	updated, err := h.findNode(strconv.FormatUint(uint64(n.ID), 10))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (h *Handler) deleteNode(c *gin.Context) {
	var n storage.Node
	if err := h.db.First(&n, c.Param("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	res := h.db.Where("id=?", c.Param("id")).Delete(&storage.Node{})
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	if err := h.db.Where("node_id=?", n.ID).Delete(&storage.NodeTokenRevocation{}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// rotateNodeToken issues a new token and briefly accepts the former token so
// an already-running agent can reconnect while replacing its configuration.
func (h *Handler) rotateNodeToken(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	token, err := RandomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	now := time.Now()
	updates := map[string]any{"old_token": n.Token, "token": token, "token_rotated_at": now}
	if err := h.db.Model(&storage.Node{}).Where("id=?", n.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	n.OldToken = n.Token
	n.Token = token
	n.TokenRotatedAt = &now
	c.JSON(http.StatusOK, n)
}

// revokeNodeToken immediately rejects a former token, closing the rotation
// window early without disturbing the currently issued token.
func (h *Handler) revokeNodeToken(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var in nodeTokenRevokeInput
	if err := c.ShouldBindJSON(&in); err != nil || in.Token == "" {
		fail(c, http.StatusBadRequest, errors.New("token is required"))
		return
	}
	if in.Token != n.OldToken {
		fail(c, http.StatusBadRequest, errors.New("token is not the current grace token"))
		return
	}
	revocation := storage.NodeTokenRevocation{NodeID: n.ID, Token: in.Token, RevokedAt: time.Now()}
	if err := h.db.Create(&revocation).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.Model(&storage.Node{}).Where("id=?", n.ID).
		Updates(map[string]any{"old_token": "", "token_rotated_at": nil}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	n.OldToken = ""
	n.TokenRotatedAt = nil
	c.JSON(http.StatusOK, n)
}

type nodeTokenRevokeInput struct {
	Token string `json:"token"`
}

// findNode looks a node up by primary key; a missing node returns errNotFound
// (an illegal id counts as missing too).
func (h *Handler) findNode(id string) (*storage.Node, error) {
	uid, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return nil, errNotFound
	}
	var n storage.Node
	if err := h.db.First(&n, uint(uid)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound
		}
		return nil, err
	}
	return &n, nil
}

// normalizeConfig guarantees the template is stored as compact JSON text; an
// empty-object input falls back to {}.
func normalizeConfig(cfg string) string {
	trimmed := strings.TrimSpace(cfg)
	if trimmed == "" {
		return "{}"
	}
	var v any
	if json.Unmarshal([]byte(trimmed), &v) != nil {
		return trimmed // validateNodeInput already guarantees validity; defensive here
	}
	compact, err := json.Marshal(v)
	if err != nil {
		return trimmed
	}
	return string(compact)
}

// nodeConfigFields holds the shared fields the panel reads out of the protocol
// config template (same shape as nodeConfig in the sub package on the
// subscription side, restricted to the subset validation needs).
type nodeConfigFields struct {
	Password string `json:"password"`
}

// parseNodeConfig pulls the shared fields out of the config template; empty or
// {} returns zero values, and invalid JSON errors (validateNodeInput already
// checked up front; this covers the update path where config is absent).
func parseNodeConfig(cfg string) (nodeConfigFields, error) {
	var out nodeConfigFields
	trimmed := strings.TrimSpace(cfg)
	if trimmed == "" || trimmed == "{}" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return out, errors.New("config must be a JSON object")
	}
	return out, nil
}

// metaUpdates turns the non-empty metadata fields of NodeInput into column
// updates; an empty string means keep the current value.
func metaUpdates(in *model.NodeInput) map[string]any {
	out := map[string]any{}
	if in.Role != "" {
		out["role"] = in.Role
	}
	if in.Direction != "" {
		out["direction"] = in.Direction
	}
	if in.LineType != "" {
		out["line_type"] = in.LineType
	}
	if in.Region != "" {
		out["region"] = in.Region
	}
	if in.City != "" {
		out["city"] = in.City
	}
	if in.Datacenter != "" {
		out["datacenter"] = in.Datacenter
	}
	if in.ISP != "" {
		out["isp"] = in.ISP
	}
	if in.Transport != "" {
		out["transport"] = in.Transport
	}
	return out
}

// applyMetaInput stores metadata from the request at node creation; any
// metadata present counts as a panel takeover.
func applyMetaInput(n *storage.Node, in *model.NodeInput) {
	provided := false
	set := func(dst *string, v string) {
		if v != "" {
			*dst = strings.TrimSpace(v)
			provided = true
		}
	}
	set(&n.Role, in.Role)
	set(&n.Direction, in.Direction)
	set(&n.LineType, in.LineType)
	set(&n.Region, in.Region)
	set(&n.City, in.City)
	set(&n.Datacenter, in.Datacenter)
	set(&n.ISP, in.ISP)
	set(&n.Transport, in.Transport)
	if provided {
		n.MetaInit = true
	}
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
		return errors.New("protocol must be one of vless/vmess/trojan/shadowsocks/hysteria2")
	}
	if err := validateNodeConfig(in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Config) != "" && !json.Valid([]byte(in.Config)) {
		return errors.New("config must be valid JSON")
	}
	for _, v := range []string{in.Role} {
		switch v {
		case "", "entry", "landing", "both":
		default:
			return errors.New("role must be entry/landing/both")
		}
	}
	switch in.Direction {
	case "", "out", "in", "both":
	default:
		return errors.New("direction must be out/in/both")
	}
	switch in.Transport {
	case "", "tls", "quic", "ws-tls", "ssh":
	default:
		return errors.New("transport must be tls/quic/ws-tls/ssh")
	}
	return nil
}

// validateNodeConfig checks the required fields of the config template per
// protocol (same rule as the subscription side: a missing field only errors
// when a subscription/share link is generated, so this catches it early at
// node create/update). The remaining protocols (vless/vmess/trojan/ss) have
// their required fields checked on the subscription side and are not repeated
// here.
func validateNodeConfig(in *model.NodeInput) error {
	if in.Protocol != model.ProtoHysteria2 {
		return nil
	}
	cfg, err := parseNodeConfig(in.Config)
	if err != nil {
		return err
	}
	if cfg.Password == "" {
		return errors.New("hysteria2 node requires config.password")
	}
	return nil
}

// fail emits the uniform error structure; internal errors are only logged and
// never leak details.
func fail(c *gin.Context, status int, err error) {
	if status >= 500 {
		c.Error(err) // gin logging
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
