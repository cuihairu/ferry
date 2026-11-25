package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/cost"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Savings report (SAVE-7): save_stats read back as daily aggregates; the converted
// cost is computed on the fly from each node's traffic unit price (only
// pay-per-traffic nodes have marginal cost, same basis as the cost dashboard).

// saveStats GET /api/save-stats?days=30 -- per-day rows plus fleet total.
func (h *Handler) saveStats(c *gin.Context) {
	days := 30
	if v := c.Query("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 366 {
			fail(c, http.StatusBadRequest, errors.New("days must be 1-366"))
			return
		}
		days = n
	}
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	var stats []storage.SaveStat
	if err := h.db.Where("day >= ?", since).Order("day ASC, node_id ASC").Find(&stats).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var nodes []storage.Node
	if err := h.db.Find(&nodes).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	byID := map[uint]storage.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	rows := make([]gin.H, 0, len(stats))
	var totDirect, totBlocked, totCache, totCents int64
	for _, s := range stats {
		n := byID[s.NodeID]
		var cents int64
		if n.BillingType == "按流量" {
			cents = int64(float64(s.DirectBytes+s.BlockedBytes+s.CacheHitBytes) / cost.BytesPerGB * float64(n.TrafficPriceCents))
		}
		rows = append(rows, gin.H{
			"node_id": s.NodeID, "name": n.Name, "day": s.Day,
			"direct_bytes": s.DirectBytes, "blocked_bytes": s.BlockedBytes,
			"cache_hit_bytes": s.CacheHitBytes, "cost_cents": cents,
		})
		totDirect += s.DirectBytes
		totBlocked += s.BlockedBytes
		totCache += s.CacheHitBytes
		totCents += cents
	}
	c.JSON(http.StatusOK, gin.H{
		"days": days,
		"rows": rows,
		"total": gin.H{
			"direct_bytes": totDirect, "blocked_bytes": totBlocked,
			"cache_hit_bytes": totCache, "cost_cents": totCents,
		},
	})
}
