package handler

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// upgradeAckTimeout covers the agent-side acceptance decision (download runs in the background, not awaited here).
const upgradeAckTimeout = 15 * time.Second

// nodeUpgrade issues a self-upgrade command (POST /api/nodes/:id/upgrade {version, url, sha256?}):
// the agent replies accept/reject immediately; the actual upgrade result shows up as a hello version change.
func (h *Handler) nodeUpgrade(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var in struct {
		Version string `json:"version"`
		URL     string `json:"url"`
		Sha256  string `json:"sha256"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Version == "" || in.URL == "" {
		fail(c, http.StatusBadRequest, errors.New("version and url are required"))
		return
	}
	if _, err := url.ParseRequestURI(in.URL); err != nil {
		fail(c, http.StatusBadRequest, errors.New("url must be absolute"))
		return
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("upg-%d-%d", n.ID, time.Now().UnixNano()),
		agentproto.MsgUpgrade,
		agentproto.Upgrade{Version: in.Version, URL: in.URL, Sha256: in.Sha256},
	)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	reply, err := h.hub.Request(int64(n.ID), env, upgradeAckTimeout)
	if err != nil {
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
	var ack agentproto.UpgradeAck
	if err := reply.Decode(&ack); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	if !ack.OK {
		fail(c, http.StatusBadRequest, errors.New("agent rejected: "+ack.Error))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"node_id": n.ID, "version": ack.Version, "accepted": true,
		"current_version": n.AgentVersion,
	})
}

// recordUpgradeStatus stores one self-upgrade progress frame (F-0712); storage failures are
// logged, never blocking the heartbeat loop.
func (h *Handler) recordUpgradeStatus(nodeID int64, us agentproto.UpgradeStatus) {
	row := storage.NodeUpgrade{
		NodeID: uint(nodeID), UpgradeID: us.UpgradeID, Version: us.Version,
		State: us.State, Error: us.Error,
	}
	if err := h.db.Create(&row).Error; err != nil {
		log.Printf("record upgrade status node=%d: %v", nodeID, err)
	}
}

// listNodeUpgrades serves GET /api/nodes/:id/upgrades: the node's self-upgrade progress rows,
// newest first, capped at limit (default 100).
func (h *Handler) listNodeUpgrades(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	limit := 100
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-500"))
			return
		}
		limit = n
	}
	out := []storage.NodeUpgrade{}
	if err := h.db.Where("node_id=?", n.ID).Order("id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"upgrades": out})
}
