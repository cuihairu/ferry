package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Manual health-state override (F-0932/F-0933): an operator can pin a node's
// derived health to healthy or degraded until an expiry. Every override must
// record why, who set it and when it lapses -- overrides without an audit
// trail are how "temporarily forced green" quietly becomes permanent.

type healthOverrideInput struct {
	State     string    `json:"state"`
	Reason    string    `json:"reason"`
	Operator  string    `json:"operator"`
	ExpiresAt time.Time `json:"expires_at"`
}

// validateHealthOverride pins the F-0933 contract: state is healthy/degraded
// (unknown is what the derivation says when it knows nothing -- forcing it
// says nothing), reason and operator are non-empty, expiry is in the future.
func validateHealthOverride(in healthOverrideInput) (storage.HealthOverride, error) {
	if in.State != "healthy" && in.State != "degraded" {
		return storage.HealthOverride{}, errors.New("state must be healthy/degraded")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return storage.HealthOverride{}, errors.New("reason is required")
	}
	if strings.TrimSpace(in.Operator) == "" {
		return storage.HealthOverride{}, errors.New("operator is required")
	}
	if in.ExpiresAt.IsZero() || !in.ExpiresAt.After(time.Now()) {
		return storage.HealthOverride{}, errors.New("expires_at must be in the future")
	}
	return storage.HealthOverride{
		State:     in.State,
		Reason:    strings.TrimSpace(in.Reason),
		Operator:  strings.TrimSpace(in.Operator),
		ExpiresAt: in.ExpiresAt.UTC(),
	}, nil
}

// setHealthOverride upserts the node's override (PUT /api/nodes/:id/health_override).
func (h *Handler) setHealthOverride(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var in healthOverrideInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	row, err := validateHealthOverride(in)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	row.NodeID = n.ID
	// One row per node: replace in place, so a second override swaps
	// reason/operator/expiry and the audit trail stays one row per event.
	if err := h.db.Where("node_id = ?", n.ID).Delete(&storage.HealthOverride{}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// getHealthOverride returns the node's override row or 404 (GET).
func (h *Handler) getHealthOverride(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var row storage.HealthOverride
	if err := h.db.Where("node_id = ?", n.ID).First(&row).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("no override set"))
		return
	}
	c.JSON(http.StatusOK, row)
}

// deleteHealthOverride removes the node's override; the derived state resumes
// on the next evaluation (DELETE).
func (h *Handler) deleteHealthOverride(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	res := h.db.Where("node_id = ?", n.ID).Delete(&storage.HealthOverride{})
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, errors.New("no override set"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}
