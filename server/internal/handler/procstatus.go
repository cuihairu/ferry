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

// 被管进程状态落库与查询（SAVE-3）：心跳携带的 ProcStatus 逐节点逐进程
// upsert（nginx cache/Squid 等入口缓存进程的命中统计随之落库），dash 经
// GET /api/nodes/:id/procs 回读。

// saveProcStatuses 把心跳里的进程快照 upsert 进 node_proc_statuses。
// 失败返回错误由调用方记日志，不阻断心跳应答。
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
		if err := h.db.Model(&row).Updates(map[string]any{
			"state": ps.State, "pid": ps.PID, "restarts": ps.Restarts,
			"metrics": metrics, "updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// nodeProcRow 是 API 返回行：metrics 列解析回数字对象（坏 JSON 省略）。
type nodeProcRow struct {
	Proc      string            `json:"proc"`
	State     string            `json:"state"`
	PID       int               `json:"pid,omitempty"`
	Restarts  int               `json:"restarts"`
	Metrics   map[string]uint64 `json:"metrics,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// nodeProcs 节点被管进程状态（GET /api/nodes/:id/procs）。
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
			Restarts: r.Restarts, UpdatedAt: r.UpdatedAt,
		}
		if r.Metrics != "" {
			_ = json.Unmarshal([]byte(r.Metrics), &row.Metrics)
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, out)
}
