package handler

import (
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Node load snapshots (E-25): connection count and measured throughput (diff
// of the latest samples) yielding utilization against the plan bandwidth; dash
// warns above 80%. P0 scope: the two metrics connections plus bandwidth.
// Sampling and utilization come from storage (F-1064), shared with the
// overload monitor.

// NodeLoad is the load view of a single node.
type NodeLoad struct {
	NodeID       uint    `json:"node_id"`
	Conns        int     `json:"conns"`         // online connections in the latest sample (summed across procs)
	Mbps         float64 `json:"mbps"`          // measured throughput from diffing the two latest samples
	CapacityMbps int     `json:"capacity_mbps"` // measured calibration > plan downlink > uplink > 100 fallback
	UtilPct      int     `json:"util_pct"`      // utilization percent; -1 = no sample, not computable
}

// listLoad returns the load snapshot of every node (GET /api/load).
func (h *Handler) listLoad(c *gin.Context) {
	var nodes []storage.Node
	if err := h.db.Find(&nodes).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	samples, err := storage.LatestLoad(h.db, time.Now().Add(-storage.LoadFresh))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	out := make([]NodeLoad, 0, len(nodes))
	for _, n := range nodes {
		s := samples[n.ID]
		cap := n.CapacityMbps()
		out = append(out, NodeLoad{
			NodeID: n.ID, Conns: s.Conns, Mbps: s.Mbps, CapacityMbps: cap,
			UtilPct: storage.UtilizationPct(s.Mbps, cap, s.Conns > 0 || s.Mbps > 0),
		})
	}
	c.JSON(http.StatusOK, out)
}
