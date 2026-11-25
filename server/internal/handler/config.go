package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// configPushInput is the payload for one config push (shared by single-node and bulk, A-15).
type configPushInput struct {
	Proc    string `json:"proc"`
	Kind    string `json:"kind"`
	Payload string `json:"payload"`
}

// pushConfig pushes one process config to a node (A-18):
// persist pending -> wait for config.ack via agenthub -> set applied/failed per the result.
func (h *Handler) pushConfig(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	var in configPushInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Proc == "" || in.Payload == "" {
		fail(c, http.StatusBadRequest, errors.New("proc and payload are required"))
		return
	}
	row, status, err := h.deployConfig(uint(id), in)
	if err != nil {
		fail(c, status, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// deployConfig runs the whole single-node config push flow, delegating to the
// relaypush.Pusher implementation on the same chain (E-16b: automatic
// re-routing/repointing and manual pushes share one config delivery channel).
// When err != nil is returned, status is the matching HTTP code (transport-class
// failure); an agent ack with OK=false is a business failure that is recorded
// for the audit trail and returned with a nil err.
func (h *Handler) deployConfig(nodeID uint, in configPushInput) (storage.NodeConfig, int, error) {
	return h.pusher.Push(nodeID, in.Proc, in.Kind, in.Payload)
}

// finishNodeConfig writes the final push result back onto the record; it delegates to the pusher's same-chain implementation.
func (h *Handler) finishNodeConfig(row *storage.NodeConfig, status string, reverted, validated bool, errMsg string) {
	h.pusher.Finish(row, status, reverted, validated, errMsg)
}

// listNodeTraffic returns a node's traffic accounting (newest first, 200 rows by default).
func (h *Handler) listNodeTraffic(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	limit := 200
	if v := c.Query("limit"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > 1000 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-1000"))
			return
		}
		limit = n
	}
	out := []storage.NodeTrafficLog{}
	if err := h.db.Where("node_id=?", id).Order("recorded_at DESC, id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// listNodeConfigs returns a node's config push history (newest first, 50 rows by default).
func (h *Handler) listNodeConfigs(c *gin.Context) {
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
	if err := h.db.Where("node_id=?", id).Order("id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
