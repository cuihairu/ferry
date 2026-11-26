package handler

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Traffic ledger reconciliation (F-1253): the user ledger (traffic_logs, one
// attribution per flow under single-end metering, F-1242/F-1243) is compared
// node-by-node against the node ledger (node_traffic_logs, the raw per-proc
// metering) over a window. Single-end metering guarantees one invariant: a node
// can never bill its users more bytes than it itself transferred -- a breach is
// flagged double_billing. The reverse (node metered far more than billed) is the
// designed norm on tunnel/landing sides and for non-user procs, so it only shows
// as a negative delta, not a flag: the report surfaces numbers for review, it
// does not judge the healthy asymmetry. A ledger row whose node was deleted
// since reports an empty name.
type trafficReconRow struct {
	NodeID      uint   `json:"node_id"`
	NodeName    string `json:"node_name"`
	UserBilled  int64  `json:"user_billed"`
	NodeMetered int64  `json:"node_metered"`
	Delta       int64  `json:"delta"` // node_metered - user_billed
	Status      string `json:"status"`
}

// trafficReconReport is the GET /api/traffic-recon envelope.
type trafficReconReport struct {
	WindowDays int               `json:"window_days"`
	Nodes      []trafficReconRow `json:"nodes"`
	Flagged    int               `json:"flagged"`
}

// trafficRecon handles GET /api/traffic-recon?days=N (default 7, cap 90): the
// flow-account audit view. Both ledgers are summed per node inside the window
// and merged; rows land sorted by node id so the dash view is stable.
func (h *Handler) trafficRecon(c *gin.Context) {
	days := 7
	if v := c.Query("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 90 {
			fail(c, http.StatusBadRequest, errors.New("days must be 1-90"))
			return
		}
		days = n
	}
	since := time.Now().AddDate(0, 0, -days)

	var billed []struct {
		NodeID uint
		Sum    int64
	}
	if err := h.db.Model(&storage.TrafficLog{}).
		Select("node_id, SUM(rx_bytes + tx_bytes) AS sum").
		Where("node_id IS NOT NULL AND recorded_at >= ?", since).
		Group("node_id").Scan(&billed).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var metered []struct {
		NodeID uint
		Sum    int64
	}
	if err := h.db.Model(&storage.NodeTrafficLog{}).
		Select("node_id, SUM(rx_bytes + tx_bytes) AS sum").
		Where("recorded_at >= ?", since).
		Group("node_id").Scan(&metered).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	rows := map[uint]*trafficReconRow{}
	ids := map[uint]bool{}
	touch := func(id uint) *trafficReconRow {
		ids[id] = true
		r := rows[id]
		if r == nil {
			r = &trafficReconRow{NodeID: id}
			rows[id] = r
		}
		return r
	}
	for _, b := range billed {
		touch(b.NodeID).UserBilled = b.Sum
	}
	for _, m := range metered {
		touch(m.NodeID).NodeMetered = m.Sum
	}

	var nodes []storage.Node
	if len(ids) > 0 {
		idList := make([]uint, 0, len(ids))
		for id := range ids {
			idList = append(idList, id)
		}
		if err := h.db.Where("id IN ?", idList).Find(&nodes).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	byID := map[uint]string{}
	for _, n := range nodes {
		byID[n.ID] = n.Name
	}

	out := make([]trafficReconRow, 0, len(rows))
	flagged := 0
	for _, r := range rows {
		r.NodeName = byID[r.NodeID]
		r.Delta = r.NodeMetered - r.UserBilled
		if r.UserBilled > r.NodeMetered {
			r.Status = "double_billing"
			flagged++
		} else {
			r.Status = "ok"
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	c.JSON(http.StatusOK, trafficReconReport{WindowDays: days, Nodes: out, Flagged: flagged})
}
