package handler

import (
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 节点负载快照（E-25）：连接数与实测吞吐（最近采样差分），
// 对套餐带宽得利用率，超 80% 由 dash 预警。P0 口径：连接数+带宽两项。

// loadFresh 是负载采样的时限，超龄视为无采样。
const loadFresh = 30 * time.Minute

// NodeLoad 是一个节点的负载视图。
type NodeLoad struct {
	NodeID       uint    `json:"node_id"`
	Conns        int     `json:"conns"`         // 最新采样在线连接数（各进程求和）
	Mbps         float64 `json:"mbps"`          // 最近两条采样差分的实测吞吐
	CapacityMbps int     `json:"capacity_mbps"` // 实测校准 > 套餐下行 > 上行 > 100 兜底
	UtilPct      int     `json:"util_pct"`      // 利用率百分比，-1=无采样不可算
}

// listLoad 返回全部节点的负载快照（GET /api/load）。
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

	// 每节点每进程取最新两条：末条出连接数，两条差分出吞吐。
	type sample struct{ rx, tx, conns int64; at time.Time }
	latest := map[uint]map[string][2]sample{} // node → proc → 最近两条
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
		row := NodeLoad{NodeID: n.ID, UtilPct: -1}
		switch {
		case n.SpeedMeasuredMbps > 0:
			row.CapacityMbps = n.SpeedMeasuredMbps
		case n.BwDownMbps > 0:
			row.CapacityMbps = n.BwDownMbps
		case n.BwUpMbps > 0:
			row.CapacityMbps = n.BwUpMbps
		default:
			row.CapacityMbps = 100
		}
		var mbps float64
		for _, pair := range latest[n.ID] {
			row.Conns += int(pair[1].conns)
			dt := pair[1].at.Sub(pair[0].at).Seconds()
			if dt < 10 || pair[0].at.IsZero() {
				continue // 采样过密或只有一条，差分不可信
			}
			mbps += float64(pair[1].rx-pair[0].rx+pair[1].tx-pair[0].tx) * 8 / dt / 1e6
		}
		row.Mbps = mbps
		if row.Conns > 0 || mbps > 0 {
			row.UtilPct = int(mbps / float64(row.CapacityMbps) * 100)
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, out)
}
