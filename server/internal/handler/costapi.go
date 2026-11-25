package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/cost"
	"github.com/gin-gonic/gin"
)

// Cost dashboard (E-23): monthly cost accounting data and high-cost alert threshold setting.
// The alert check runs periodically via cost.Loop; here we read the report and change the threshold.

// getCost returns the cost dashboard data (month-to-date traffic spend / fixed cost / projection + dimension summary + threshold).
func (h *Handler) getCost(c *gin.Context) {
	rep, err := cost.Build(h.db, time.Now())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rep)
}

// putCostThreshold sets the high-cost alert threshold (cents/month).
func (h *Handler) putCostThreshold(c *gin.Context) {
	var in struct {
		Cents *int64 `json:"cents"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Cents == nil {
		fail(c, http.StatusBadRequest, errors.New("cents is required"))
		return
	}
	if err := cost.SaveThreshold(h.db, *in.Cents); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cents": *in.Cents})
}
