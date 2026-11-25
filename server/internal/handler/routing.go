package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/routing"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// rulelibAckTimeout uses the same budget as config.push: it covers the worst
// case for the agent-side write plus reload.
const rulelibAckTimeout = 60 * time.Second

// rulelibNameRe constrains rule library file names: it guards against path
// traversal by allowing only a single bare file name.
var (
	rulelibNameRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	rulelibTraversal = regexp.MustCompile(`\.\.`)
)

// adsSettingKey is the setting key for the ad blocking switch (SAVE-4): on by
// default, "0"/"false" turns it off.
const adsSettingKey = "routing_ads_enabled"

// adsEnabled reads the ad blocking switch; unset means on (the design default
// ships with the open source rule lists).
func adsEnabled(db *gorm.DB) bool {
	v, ok, err := storage.GetSetting(db, adsSettingKey)
	if err != nil || !ok {
		return true
	}
	return v != "0" && v != "false"
}

// routingSets assembles the rule set list used for rendering according to the
// switch: AdBlock is filtered out when ad blocking is off.
func (h *Handler) routingSets() []routing.RuleSet {
	sets := routing.Sets()
	if !adsEnabled(h.db) {
		return routing.WithoutAds(sets)
	}
	return sets
}

// renderRoutingConfig returns the full config produced by merging the split-rule
// sections into the node's config template (SAVE-1). It only renders and never
// persists: delivery goes through the existing POST /api/nodes/:id/config.
func (h *Handler) renderRoutingConfig(c *gin.Context) {
	var n storage.Node
	if err := h.db.First(&n, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	// The routing rule section is xray kernel syntax (routing.rules / outbounds
	// tags); the hysteria2 kernel config is YAML, and split rules do not follow
	// xray syntax, so rendering it directly would produce an invalid config —
	// report it honestly instead of stitching out a misleading result (hy2 batch).
	if n.Protocol == "hysteria2" {
		fail(c, http.StatusBadRequest, errors.New("routing merge only supports xray kernel config; "+
			"hysteria2 config is YAML and must be pushed as-is via POST /api/nodes/:id/config"))
		return
	}
	sets := h.routingSets()
	merged, err := routing.Merge(n.Config, sets)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	sum := sha256.Sum256([]byte(merged))
	c.JSON(http.StatusOK, gin.H{
		"node_id": n.ID,
		"sha256":  hex.EncodeToString(sum[:]),
		"config":  merged,
		"sets":    sets,
	})
}

// putAdsEnabled is the ad/tracker blocking switch (PUT /api/routing/ads
// {enabled}): once off, rendered configs no longer contain the blocking rule
// sections; already-pushed configs are left untouched (re-render and re-push
// for it to take effect).
func (h *Handler) putAdsEnabled(c *gin.Context) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Enabled == nil {
		fail(c, http.StatusBadRequest, errors.New("enabled is required"))
		return
	}
	v := "0"
	if *in.Enabled {
		v = "1"
	}
	if err := storage.SetSetting(h.db, adsSettingKey, v); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": *in.Enabled})
}

// pushRuleLib distributes a rule library data file to a node (geoip.dat/
// geosite.dat etc.): the raw request body is the file content, which goes over
// the config.push channel (kind=rulelib:<name>), is written into the agent's
// asset directory and triggers a reload. Every push leaves a version snapshot in
// node_configs.
func (h *Handler) pushRuleLib(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	proc := c.Query("proc")
	name := c.Query("name")
	if proc == "" {
		fail(c, http.StatusBadRequest, errors.New("proc is required"))
		return
	}
	if !rulelibNameRe.MatchString(name) || rulelibTraversal.MatchString(name) {
		fail(c, http.StatusBadRequest, fmt.Errorf("invalid rulelib name %q", name))
		return
	}
	var n storage.Node
	if err := h.db.First(&n, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	payload, err := c.GetRawData()
	if err != nil || len(payload) == 0 {
		fail(c, http.StatusBadRequest, errors.New("request body is required"))
		return
	}

	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	kind := "rulelib:" + name
	row := storage.NodeConfig{
		NodeID: uint(id), Proc: proc, Kind: kind,
		Version: digest, Sha256: digest, Payload: string(payload),
		Status: "pending",
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("rulelib-%d-%d", id, time.Now().UnixNano()),
		agentproto.MsgConfigPush,
		agentproto.ConfigPush{Proc: proc, Kind: kind, Version: digest, Sha256: digest, Payload: string(payload)},
	)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	reply, err := h.hub.Request(int64(id), env, rulelibAckTimeout)
	if err != nil {
		h.finishNodeConfig(&row, "failed", false, false, err.Error())
		switch {
		case errors.Is(err, agenthub.ErrOffline):
			fail(c, http.StatusBadGateway, err)
		case errors.Is(err, agenthub.ErrTimeout):
			fail(c, http.StatusGatewayTimeout, err)
		default:
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}

	var ack agentproto.ConfigAck
	if err := reply.Decode(&ack); err != nil {
		h.finishNodeConfig(&row, "failed", false, false, "bad config_ack: "+err.Error())
		fail(c, http.StatusBadGateway, err)
		return
	}
	status := "failed"
	if ack.OK {
		status = "applied"
	}
	h.finishNodeConfig(&row, status, ack.Reverted, ack.Validated, ack.Error)
	row.Payload = "" // the version history does not carry file contents
	c.JSON(http.StatusOK, row)
}

// listRuleLib returns a node's rule library version history (node_configs
// snapshots, without file contents).
func (h *Handler) listRuleLib(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > 200 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-200"))
			return
		}
		limit = n
	}
	out := []storage.NodeConfig{}
	if err := h.db.
		Select("id", "node_id", "proc", "kind", "version", "sha256", "status", "reverted", "validated", "error", "created_at", "updated_at").
		Where("node_id=? AND kind LIKE 'rulelib:%'", id).
		Order("id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
