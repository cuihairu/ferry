package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Entry pool viewing and manual reset (E-16): suspend/attach decisions run
// periodically in internal/pool; this gives the dash its pool view and the
// manual re-attach when every probe source is down.

// poolView is one row of the entry pool: the node itself plus a summary of the latest probe verdict within the window.
type poolView struct {
	storage.Node
	LastVerdict string     `json:"last_verdict"` // healthy/sick; empty = no verdict within the window
	LastProbed  *time.Time `json:"last_probed"`
}

// listPool is the entry pool list (GET /api/pool): nodes with role entry/both that are enabled.
func (h *Handler) listPool(c *gin.Context) {
	var nodes []storage.Node
	if err := h.db.Where("enabled = ? AND role IN (?, ?)", true, "entry", "both").
		Order("id").Find(&nodes).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	since := time.Now().Add(-pool.ProbeFresh)
	out := make([]poolView, 0, len(nodes))
	for _, n := range nodes {
		v := poolView{Node: n}
		var r storage.ProbeReport
		err := h.db.Where("target_node_id = ? AND probed_at > ?", n.ID, since).
			Order("probed_at DESC, id DESC").First(&r).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			// no verdict within the window, leave blank
		case err != nil:
			fail(c, http.StatusInternalServerError, err)
			return
		default:
			v.LastVerdict = r.Verdict
			v.LastProbed = &r.ProbedAt
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, out)
}

// suspendPoolNode suspends manually (POST /api/pool/:id/suspend): it leaves
// the pool immediately with a trace, and the subscription entry pool drops it
// right away; re-suspending is 409, non-entry nodes 400.
func (h *Handler) suspendPoolNode(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	node, err := pool.Suspend(h.db, uint(id), nil)
	switch {
	case err == nil:
		if pool.OnChange != nil {
			go pool.OnChange(h.db) // E-27 per-region reconciliation: any pool state transition triggers reconciliation
		}
		c.JSON(http.StatusOK, node)
	case errors.Is(err, pool.ErrAlreadySuspended):
		c.JSON(http.StatusConflict, gin.H{"error": "节点已在摘除状态"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	default:
		fail(c, http.StatusBadRequest, err)
	}
}

// resumePoolNode resumes manually (POST /api/pool/:id/resume): a suspended
// node is re-attached and a standby node returns to the pool; the other
// codes (404/409) match the enumeration-protection convention.
func (h *Handler) resumePoolNode(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	node, err := pool.Resume(h.db, uint(id), nil)
	switch {
	case err == nil:
		if pool.OnChange != nil {
			go pool.OnChange(h.db) // E-27 per-region reconciliation: any pool state transition triggers reconciliation
		}
		c.JSON(http.StatusOK, node)
	case errors.Is(err, pool.ErrNotSuspended):
		c.JSON(http.StatusConflict, gin.H{"error": "节点不在摘除/预备状态"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	default:
		fail(c, http.StatusInternalServerError, err)
	}
}

// standbyPoolNode marks standby (POST /api/pool/:id/standby, BR-3): an in-pool
// entry goes to standby -- out of subscriptions but still online -- and is
// promoted back when the pipeline L2 backfill resumes; re-marking is 409.
func (h *Handler) standbyPoolNode(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	node, err := pool.MarkStandby(h.db, uint(id))
	switch {
	case err == nil:
		if pool.OnChange != nil {
			go pool.OnChange(h.db) // E-27 per-region reconciliation: any pool state transition triggers reconciliation
		}
		c.JSON(http.StatusOK, node)
	case errors.Is(err, pool.ErrNotActive):
		c.JSON(http.StatusConflict, gin.H{"error": "节点不在池内（已摘除或已是预备）"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	default:
		fail(c, http.StatusBadRequest, err)
	}
}
