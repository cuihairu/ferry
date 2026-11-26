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

// loadFresh is the sample freshness window; older samples count as absent.
const loadFresh = 30 * time.Minute

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
	since := time.Now().Add(-loadFresh)
	var logs []storage.NodeTrafficLog
	if err := h.db.Where("recorded_at > ?", since).
		Order("recorded_at ASC, id ASC").Find(&logs).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	// Keep the two latest samples per node per proc: the last one gives the
	// connection count, the pair diffs into throughput.
	type sample struct {
		rx, tx, conns int64
		at            time.Time
	}
	latest := map[uint]map[string][2]sample{} // node -> proc -> two latest
	for _, l := range logs {
		if latest[l.NodeID] == nil {
			latest[l.NodeID] = map[string][2]sample{}
		}
		pair := latest[l.NodeID][l.Proc]
		pair[0], pair[1] = pair[1], sample{int64(l.RxBytes), int64(l.TxBytes), int64(l.Conns), l.RecordedAt}
		latest[l.NodeID][l.Proc] = pair
	}

	out := make([]NodeLoad, 0, len(nodes))
	for _, n := range nodes {
		row := NodeLoad{NodeID: n.ID, UtilPct: -1, CapacityMbps: n.CapacityMbps()}
		var mbps float64
		for _, pair := range latest[n.ID] {
			row.Conns += int(pair[1].conns)
			dt := pair[1].at.Sub(pair[0].at).Seconds()
			if dt < 10 || pair[0].at.IsZero() {
				continue // samples too close together or only one of them; diff unreliable
			}
			mbps += float64(pair[1].rx-pair[0].rx+pair[1].tx-pair[0].tx) * 8 / dt / 1e6
		}
		row.Mbps = mbps
		row.UtilPct = storage.UtilizationPct(mbps, row.CapacityMbps, row.Conns > 0 || mbps > 0)
		out = append(out, row)
	}
	c.JSON(http.StatusOK, out)
}
