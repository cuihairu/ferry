package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Managed process status persistence and queries (SAVE-3): the ProcStatus list
// carried in heartbeats is upserted per node per process (hit counters for entry
// cache processes like nginx cache/Squid are persisted along with it), and the
// dash reads them back via GET /api/nodes/:id/procs.

// saveProcStatuses upserts the process snapshot from a heartbeat into
// node_proc_statuses. On failure it returns the error for the caller to log;
// it does not block the heartbeat reply.
func (h *Handler) saveProcStatuses(nodeID int64, procs []agentproto.ProcStatus) error {
	for _, ps := range procs {
		if ps.Name == "" {
			continue
		}
		metrics := ""
		if len(ps.Metrics) > 0 {
			if b, err := json.Marshal(ps.Metrics); err == nil {
				metrics = string(b)
			}
		}
		var row storage.NodeProcStatus
		err := h.db.Where("node_id=? AND proc=?", nodeID, ps.Name).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = storage.NodeProcStatus{
				NodeID: uint(nodeID), Proc: ps.Name, State: ps.State,
				PID: ps.PID, Restarts: ps.Restarts, Metrics: metrics,
				Version: ps.Version,
				UpdatedAt: time.Now(),
			}
			if err := h.db.Create(&row).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		updates := map[string]any{
			"state": ps.State, "pid": ps.PID, "restarts": ps.Restarts,
			"metrics": metrics, "updated_at": time.Now(),
		}
		if ps.Version != "" {
			updates["version"] = ps.Version
		} // an older agent's empty version must not erase a known one
		if err := h.db.Model(&row).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}

// nodeProcRow is one API response row: the metrics column is parsed back into
// a numeric map (skipped when the JSON is bad).
type nodeProcRow struct {
	Proc      string            `json:"proc"`
	State     string            `json:"state"`
	PID       int               `json:"pid,omitempty"`
	Restarts  int               `json:"restarts"`
	Metrics   map[string]uint64 `json:"metrics,omitempty"`
	Version   string            `json:"version,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// nodeProcs returns a node's managed process statuses (GET /api/nodes/:id/procs).
func (h *Handler) nodeProcs(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var rows []storage.NodeProcStatus
	if err := h.db.Where("node_id=?", n.ID).Order("proc ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]nodeProcRow, 0, len(rows))
	for _, r := range rows {
		row := nodeProcRow{
			Proc: r.Proc, State: r.State, PID: r.PID,
			Restarts: r.Restarts, Version: r.Version, UpdatedAt: r.UpdatedAt,
		}
		if r.Metrics != "" {
			_ = json.Unmarshal([]byte(r.Metrics), &row.Metrics)
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, out)
}
