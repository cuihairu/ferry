package handler

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 告警状态取值。
const (
	AlertStateActive   = "active"
	AlertStateResolved = "resolved"
)

// recordAlert 落一条告警：同节点同类别同进程的活跃告警只保留一条，
// 重复上报刷新消息与时间（agent 侧自带节流，面板侧再兜一层去重）。
func (h *Handler) recordAlert(nodeID uint, al agentproto.Alarm) {
	sev := al.Severity
	if sev == "" {
		sev = agentproto.AlarmSeverityWarning
	}
	var row storage.Alert
	err := h.db.Where("node_id=? AND kind=? AND proc=? AND state=?",
		nodeID, al.Kind, al.Proc, AlertStateActive).First(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		row = storage.Alert{
			NodeID: nodeID, Kind: al.Kind, Severity: sev,
			Proc: al.Proc, Message: al.Message, State: AlertStateActive,
		}
		if err := h.db.Create(&row).Error; err != nil {
			log.Printf("create alert node=%d kind=%s: %v", nodeID, al.Kind, err)
			return
		}
		// 告警激活即落事件（HERALD-3 余量）：进程崩溃拉起失败投 Herald；
		// 活跃告警唯一性即去重闸门（恢复消解后复发才再发）。失败只记日志。
		if al.Kind == agentproto.AlarmKindProcCrash {
			if _, err := herald.Emit(h.db, herald.EmitInput{
				Kind: herald.KindProcCrashed, Severity: herald.SeverityCritical,
				Title:    fmt.Sprintf("节点 %d 进程 %s 崩溃且拉起失败", nodeID, al.Proc),
				Body:     al.Message,
				Target:   herald.TargetAdmin,
				DedupKey: fmt.Sprintf("node:%d:proc_crashed:%s", nodeID, al.Proc),
				Meta:     map[string]any{"node_id": nodeID, "proc": al.Proc, "severity": sev},
			}); err != nil {
				log.Printf("herald emit proc_crashed: node=%d %v", nodeID, err)
			}
		}
	case err != nil:
		log.Printf("find active alert node=%d kind=%s: %v", nodeID, al.Kind, err)
	default:
		row.Message = al.Message
		row.Severity = sev
		row.CreatedAt = time.Now()
		if err := h.db.Save(&row).Error; err != nil {
			log.Printf("refresh alert id=%d: %v", row.ID, err)
		}
	}
}

// resolveProcCrashAlerts 进程恢复运行时自动消解对应的崩溃告警（A-22）。
func (h *Handler) resolveProcCrashAlerts(nodeID uint, proc string) {
	now := time.Now()
	res := h.db.Model(&storage.Alert{}).
		Where("node_id=? AND kind=? AND proc=? AND state=?",
			nodeID, agentproto.AlarmKindProcCrash, proc, AlertStateActive).
		Updates(map[string]any{"state": AlertStateResolved, "resolved_at": now})
	if res.Error != nil {
		log.Printf("resolve proc_crash alerts node=%d proc=%s: %v", nodeID, proc, res.Error)
	}
}

// listAlerts 返回告警列表（新在前，默认 100 条）：可按 state/node_id/kind 过滤。
func (h *Handler) listAlerts(c *gin.Context) {
	limit := 100
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-500"))
			return
		}
		limit = n
	}
	q := h.db.Model(&storage.Alert{})
	if v := c.Query("state"); v != "" {
		if v != AlertStateActive && v != AlertStateResolved {
			fail(c, http.StatusBadRequest, errors.New("state must be active/resolved"))
			return
		}
		q = q.Where("state=?", v)
	}
	if v := c.Query("kind"); v != "" {
		q = q.Where("kind=?", v)
	}
	if v := c.Query("node_id"); v != "" {
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("bad node_id"))
			return
		}
		q = q.Where("node_id=?", id)
	}
	out := []storage.Alert{}
	if err := q.Order("id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// resolveAlert 手动标记一条告警为已处理（POST /api/alerts/:id/resolve）。
func (h *Handler) resolveAlert(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("alert not found"))
		return
	}
	now := time.Now()
	res := h.db.Model(&storage.Alert{}).Where("id=?", id).
		Updates(map[string]any{"state": AlertStateResolved, "resolved_at": now})
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, errors.New("alert not found"))
		return
	}
	var row storage.Alert
	if err := h.db.First(&row, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}
