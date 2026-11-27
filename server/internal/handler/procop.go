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

// Node process operations and batch dispatch (A-15): proc_ctl travels the
// panel->agent request/reply path; batch dispatches to many nodes concurrently
// with a per-node reply (offline/timeout does not slow down the whole batch).

// procCtlTimeout covers the time for the agent to carry out the instruction
// (including the process start triggered by reload).
const procCtlTimeout = 15 * time.Second

// maxBatchNodes caps the number of nodes per batch, guarding against an oversized batch.
const maxBatchNodes = 50

// defaultProcName is the target process name when proc is unspecified
// (consistent with config dispatch and stats collection).
const defaultProcName = "xray"

var procActions = map[string]bool{
	agentproto.ProcActionStart:  true,
	agentproto.ProcActionStop:   true,
	agentproto.ProcActionReload: true,
}

// procCtl dispatches proc_ctl to a single node and waits for the reply.
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

// nodeProcOp single-node process operation (POST /api/nodes/:id/proc {action, proc?}).
func (h *Handler) nodeProcOp(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var in struct {
		Action  string `json:"action"`
		Proc    string `json:"proc"`
		Confirm string `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if !procActions[in.Action] {
		fail(c, http.StatusBadRequest, errors.New("action must be start/stop/reload"))
		return
	}
	// F-1709: stopping the managed proc interrupts the node's service — the
	// body must echo the node name as a second confirmation. Compared on the
	// already-bound struct: the bind consumed the body, a re-read would be EOF.
	if in.Action == "stop" && in.Confirm != n.Name {
		fail(c, http.StatusBadRequest, fmt.Errorf("confirmation mismatch: pass confirm=%s to stop the proc", n.Name))
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

// batchNodeProc batch process operation (POST /api/nodes/batch/proc {ids, action, proc?}):
// concurrent dispatch, per-node replies.
func (h *Handler) batchNodeProc(c *gin.Context) {
	var in struct {
		IDs     []uint `json:"ids"`
		Action  string `json:"action"`
		Proc    string `json:"proc"`
		Confirm string `json:"confirm"`
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
	// F-1709: a batch stop cuts service on every selected node at once — the
	// body must carry the fixed batch-stop token (no single name to echo).
	// Compared on the already-bound struct; a re-read would be EOF.
	if in.Action == "stop" && in.Confirm != "batch-stop" {
		fail(c, http.StatusBadRequest, fmt.Errorf("confirmation mismatch: pass confirm=batch-stop to batch stop procs"))
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

// batchNodeConfig dispatches the same config to many nodes (POST /api/nodes/batch/config):
// concurrent dispatch, per-node replies and a DB record of what was deployed.
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
