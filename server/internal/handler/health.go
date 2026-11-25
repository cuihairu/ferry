package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/pathhealth"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// getPathHealth reports one path's derived health state (F-0251): fresh probe
// verdicts + endpoint online/pool states, computed on read.
func (h *Handler) getPathHealth(c *gin.Context) {
	var p storage.Path
	if err := h.db.First(&p, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("path not found"))
		return
	}
	health := pathhealth.Evaluate(h.db, []storage.Path{p}, time.Now())[p.ID]
	c.JSON(http.StatusOK, gin.H{"path_id": p.ID, "health": health})
}

// getPathGroupHealth reports a group's aggregated health (F-0268) and its
// availability percentage over the stats window (F-0269). Excluded members
// do not participate; availability is null without probe data.
func (h *Handler) getPathGroupHealth(c *gin.Context) {
	g, err := h.findPathGroup(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("path group not found"))
		return
	}
	now := time.Now()
	state, members, err := pathhealth.GroupHealth(h.db, g.ID, now)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	availability, err := pathhealth.Availability(h.db, g.ID, now)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"group_id":     g.ID,
		"health":       state,
		"availability": availability,
		"members":      members,
	})
}
