package handler

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/gin-gonic/gin"
)

// 节点进程操作与批量下发（A-15）：proc_ctl 走面板→agent 请求应答通路，
// 批量对多节点并发下发、逐节点回执（离线/超时不拖慢整批）。

// procCtlTimeout 覆盖 agent 侧指令落地（含 reload 拉起）的耗时。
const procCtlTimeout = 15 * time.Second

// maxBatchNodes 限制单批节点数，防误传超大批量。
const maxBatchNodes = 50

// defaultProcName 是未指定 proc 时的目标进程名（与配置下发、stats 采集一致）。
const defaultProcName = "xray"

var procActions = map[string]bool{
	agentproto.ProcActionStart:  true,
	agentproto.ProcActionStop:   true,
	agentproto.ProcActionReload: true,
}

// procCtl 向单节点下发 proc_ctl 并等待应答。
func (h *Handler) procCtl(nodeID int64, proc, action string) (agentproto.ProcCtlAck, int, error) {
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("ctl-%d-%d", nodeID, time.Now().UnixNano()),
		agentproto.MsgProcCtl,
		agentproto.ProcCtl{Proc: proc, Action: action},
	)
	if err != nil {
		return agentproto.ProcCtlAck{}, http.StatusInternalServerError, err
	}
	reply, err := h.hub.Request(nodeID, env, procCtlTimeout)
	if err != nil {
		switch {
		case errors.Is(err, agenthub.ErrOffline):
			return agentproto.ProcCtlAck{}, http.StatusBadGateway, err
		case errors.Is(err, agenthub.ErrTimeout):
			return agentproto.ProcCtlAck{}, http.StatusGatewayTimeout, err
		default:
			return agentproto.ProcCtlAck{}, http.StatusInternalServerError, err
		}
	}
	var ack agentproto.ProcCtlAck
	if err := reply.Decode(&ack); err != nil {
		return agentproto.ProcCtlAck{}, http.StatusBadGateway, err
	}
	return ack, 0, nil
}

// nodeProcOp 单节点进程操作（POST /api/nodes/:id/proc {action, proc?}）。
func (h *Handler) nodeProcOp(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var in struct {
		Action string `json:"action"`
		Proc   string `json:"proc"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if !procActions[in.Action] {
		fail(c, http.StatusBadRequest, errors.New("action must be start/stop/reload"))
		return
	}
	if in.Proc == "" {
		in.Proc = defaultProcName
	}
	ack, status, err := h.procCtl(int64(n.ID), in.Proc, in.Action)
	if err != nil {
		fail(c, status, err)
		return
	}
	if ack.Error != "" {
		fail(c, http.StatusBadRequest, errors.New(ack.Error))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"node_id": n.ID, "proc": ack.Proc, "action": ack.Action, "ok": ack.OK,
	})
}

// batchNodeProc 批量进程操作（POST /api/nodes/batch/proc {ids, action, proc?}）：
// 并发下发，逐节点回执。
func (h *Handler) batchNodeProc(c *gin.Context) {
	var in struct {
		IDs    []uint `json:"ids"`
		Action string `json:"action"`
		Proc   string `json:"proc"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > maxBatchNodes {
		fail(c, http.StatusBadRequest, fmt.Errorf("ids must be 1-%d entries", maxBatchNodes))
		return
	}
	if !procActions[in.Action] {
		fail(c, http.StatusBadRequest, errors.New("action must be start/stop/reload"))
		return
	}
	if in.Proc == "" {
		in.Proc = defaultProcName
	}
	results := make([]gin.H, len(in.IDs))
	var wg sync.WaitGroup
	for i, id := range in.IDs {
		wg.Add(1)
		go func(i int, id uint) {
			defer wg.Done()
			r := gin.H{"node_id": id, "proc": in.Proc, "action": in.Action}
			ack, _, err := h.procCtl(int64(id), in.Proc, in.Action)
			switch {
			case err != nil:
				r["ok"] = false
				r["error"] = err.Error()
			case ack.Error != "":
				r["ok"] = false
				r["error"] = ack.Error
			default:
				r["ok"] = true
			}
			results[i] = r
		}(i, id)
	}
	wg.Wait()
	c.JSON(http.StatusOK, results)
}

// batchNodeConfig 批量下发同一份配置（POST /api/nodes/batch/config）：
// 并发下发，逐节点回执与落库留痕。
func (h *Handler) batchNodeConfig(c *gin.Context) {
	var in struct {
		IDs     []uint `json:"ids"`
		Proc    string `json:"proc"`
		Kind    string `json:"kind"`
		Payload string `json:"payload"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > maxBatchNodes {
		fail(c, http.StatusBadRequest, fmt.Errorf("ids must be 1-%d entries", maxBatchNodes))
		return
	}
	if in.Proc == "" || in.Payload == "" {
		fail(c, http.StatusBadRequest, errors.New("proc and payload are required"))
		return
	}
	one := configPushInput{Proc: in.Proc, Kind: in.Kind, Payload: in.Payload}
	results := make([]gin.H, len(in.IDs))
	var wg sync.WaitGroup
	for i, id := range in.IDs {
		wg.Add(1)
		go func(i int, id uint) {
			defer wg.Done()
			r := gin.H{"node_id": id}
			results[i] = r
			row, status, err := h.deployConfig(id, one)
			if err != nil {
				r["ok"] = false
				r["error"] = err.Error()
				r["http_status"] = status
				return
			}
			r["ok"] = true
			r["status"] = row.Status
			r["sha256"] = row.Sha256
		}(i, id)
	}
	wg.Wait()
	c.JSON(http.StatusOK, results)
}
