package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/recovery"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// listNodes returns the node list, narrowed by optional filters (F-1007~F-1010):
// capability, region, isp, asn and line_type. The four string filters are exact
// matches pushed into SQL; capability is evaluated in memory because the static
// list falls back to the role-derived set when no list is stored and the
// dynamic state may mark it off.
func (h *Handler) listNodes(c *gin.Context) {
	q := h.db.Model(&storage.Node{})
	for _, f := range []struct{ query, column string }{
		{"region", "region"},
		{"isp", "isp"},
		{"asn", "asn"},
		{"line_type", "line_type"},
	} {
		if v := strings.TrimSpace(c.Query(f.query)); v != "" {
			q = q.Where(f.column+" = ?", v)
		}
	}
	capability := strings.TrimSpace(c.Query("capability"))
	if capability != "" && !storage.KnownCapabilities[capability] {
		fail(c, http.StatusBadRequest, errors.New("unknown capability"))
		return
	}
	out := []storage.Node{}
	if err := q.Order("id").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if capability != "" {
		kept := out[:0]
		for _, n := range out {
			if storage.HasCapability(n, capability) {
				kept = append(kept, n)
			}
		}
		out = kept
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
	if pid, provided := h.resolveProviderID(c, &in); provided {
		if err := h.db.Model(n).Update("provider_id", pid).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		n.ProviderID = pid
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
	// cost fields (F-0743): pointers distinguish "absent" from "explicitly 0".
	if in.BillingType != "" {
		n.BillingType = in.BillingType
	}
	if in.TrafficPriceCents != nil {
		n.TrafficPriceCents = *in.TrafficPriceCents
	}
	if in.MonthlyCostCents != nil {
		n.MonthlyCostCents = *in.MonthlyCostCents
	}
	if in.Currency != "" {
		n.Currency = strings.TrimSpace(in.Currency)
	}
	if in.CostNote != "" {
		n.CostNote = strings.TrimSpace(in.CostNote)
	}
	// static capabilities: validated up front, so the marshal cannot fail here.
	if in.Capabilities != nil {
		raw, err := storage.ParseCapsJSON(in.Capabilities)
		if err != nil {
			return nil, err
		}
		n.Capabilities = raw
	}
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
	// Business metadata (F-0743): labels/group and the cost fields follow the
	// same keep-on-absent rule; label validation already ran in validateNodeInput.
	if in.Labels != nil {
		raw, err := storage.MarshalLabelsJSON(in.Labels)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if raw != n.Labels {
			updates["labels"] = raw
			metaProvided = true
		}
	}
	if in.Group != "" {
		updates["group"] = strings.TrimSpace(in.Group)
		metaProvided = true
	}
	if metaProvided {
		// once the panel has edited metadata, registration reports no longer override it.
		updates["meta_init"] = true
	}
	if pid, provided := h.resolveProviderID(c, &in); provided {
		updates["provider_id"] = pid
	}
	if in.BillingType != "" {
		updates["billing_type"] = in.BillingType
	}
	if in.TrafficPriceCents != nil {
		updates["traffic_price_cents"] = *in.TrafficPriceCents
	}
	if in.MonthlyCostCents != nil {
		updates["monthly_cost_cents"] = *in.MonthlyCostCents
	}
	if in.Currency != "" {
		updates["currency"] = strings.TrimSpace(in.Currency)
	}
	if in.CostNote != "" {
		updates["cost_note"] = strings.TrimSpace(in.CostNote)
	}
	// static capabilities (F-0222): nil keeps the current value; a provided list
	// replaces it, and an actual change appends a history row (F-0231).
	capChanged := ""
	if in.Capabilities != nil {
		raw, err := storage.ParseCapsJSON(in.Capabilities)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if raw != n.Capabilities {
			updates["capabilities"] = raw
			capChanged = raw
		}
	}
	if err := h.db.Model(&storage.Node{}).Where("id=?", n.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if capChanged != "" {
		h.recordCapabilityChange(n.ID, "static", beforeCapsJSON(n.Capabilities), capChanged, "panel update")
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
	// Dependency checks (F-0214): a node wired into the active forwarding
	// fabric is not deletable — remove the Path/LandingAssignment links first.
	// History rows (traffic, probes, alerts, config versions) are kept as the
	// retention rule: they stay queryable by node id after the node row is gone.
	id := c.Param("id")
	var paths int64
	if err := h.db.Model(&storage.Path{}).Where(
		"entry_node_id = ? OR landing_node_id = ? OR relay_node_id = ?", id, id, id).
		Count(&paths).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if paths > 0 {
		fail(c, http.StatusConflict, errors.New("node is used by paths; remove them first"))
		return
	}
	var assignments int64
	if err := h.db.Model(&storage.LandingAssignment{}).Where(
		"(entry_node_id = ? OR landing_node_id = ?) AND released_at IS NULL", id, id).
		Count(&assignments).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if assignments > 0 {
		fail(c, http.StatusConflict, errors.New("node has active landing assignments; release them first"))
		return
	}
	var running int64
	if err := h.db.Model(&storage.Recovery{}).Where("node_id = ? AND state = ?", id, recovery.StateRunning).
		Count(&running).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if running > 0 {
		fail(c, http.StatusConflict, errors.New("node has a running recovery; finish or fail it first"))
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

// beforeCapsJSON normalizes the stored capability list into the JSON form the
// history rows compare against (an unset list reads as an empty array).
func beforeCapsJSON(raw string) string {
	if raw == "" {
		return "[]"
	}
	return raw
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
	if in.ASN != "" {
		out["asn"] = strings.TrimSpace(in.ASN)
	}
	if in.Transport != "" {
		out["transport"] = in.Transport
	}
	return out
}

// resolveProviderID validates a provider link request (F-0207): nil keeps the
// current value, 0 clears the link, >0 must reference an existing provider.
// Returns (value, provided).
func (h *Handler) resolveProviderID(c *gin.Context, in *model.NodeInput) (*uint, bool) {
	if in.ProviderID == nil {
		return nil, false
	}
	if *in.ProviderID == 0 {
		return nil, true
	}
	var p storage.Provider
	if err := h.db.First(&p, *in.ProviderID).Error; err != nil {
		fail(c, http.StatusBadRequest, errors.New("provider not found"))
		return nil, true
	}
	return in.ProviderID, true
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
	set(&n.ASN, in.ASN)
	set(&n.Transport, in.Transport)
	if in.Group != "" {
		n.Group = strings.TrimSpace(in.Group)
	}
	if in.Labels != nil {
		// validated in validateNodeInput, so the marshal cannot fail here
		n.Labels, _ = storage.MarshalLabelsJSON(in.Labels)
	}
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
	if in.Capabilities != nil {
		if _, err := storage.ParseCapsJSON(in.Capabilities); err != nil {
			return err
		}
	}
	// Business metadata (F-0743): billing type vocabulary, non-negative prices,
	// label hygiene.
	switch in.BillingType {
	case "", "traffic", "按流量", "monthly", "包月", "fixed-bandwidth", "固定带宽":
	default:
		return errors.New("billing_type must be traffic/monthly/fixed-bandwidth")
	}
	if in.TrafficPriceCents != nil && *in.TrafficPriceCents < 0 {
		return errors.New("traffic_price_cents must be >= 0")
	}
	if in.MonthlyCostCents != nil && *in.MonthlyCostCents < 0 {
		return errors.New("monthly_cost_cents must be >= 0")
	}
	if in.Currency != "" && len(strings.TrimSpace(in.Currency)) > 8 {
		return errors.New("currency must be at most 8 characters")
	}
	for _, l := range in.Labels {
		if strings.TrimSpace(l) == "" {
			return errors.New("labels must not contain empty entries")
		}
	}
	if len(in.Labels) > 64 {
		return errors.New("labels must hold at most 64 entries")
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
