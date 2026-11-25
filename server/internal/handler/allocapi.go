package handler

import (
	"errors"
	"net/http"

	"github.com/cuihairu/ferry/server/internal/alloc"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Landing auto-assignment (E-21) admin side: policy view/set and the current
// auto assignment rows. Assignment decisions run periodically in alloc.Loop;
// here we only read results and edit the policy.

// listAlloc returns the current policy and the effective auto assignment rows.
func (h *Handler) listAlloc(c *gin.Context) {
	policy, err := alloc.LoadPolicy(h.db)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	rows := []storage.LandingAssignment{}
	if err := h.db.Where("released_at IS NULL AND strategy != ?", alloc.PolicyManual).
		Order("id DESC").Limit(500).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"policy": policy, "rows": rows})
}

// putAllocPolicy sets the assignment tiers for each direction.
func (h *Handler) putAllocPolicy(c *gin.Context) {
	var in alloc.PolicySetting
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := alloc.SavePolicy(h.db, in); err != nil {
		fail(c, http.StatusBadRequest, errors.New("policy must be least_conn/cost_first/perf_first/balanced"))
		return
	}
	c.JSON(http.StatusOK, in)
}
