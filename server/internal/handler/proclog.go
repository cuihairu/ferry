package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/gin-gonic/gin"
)

// Node process runtime logs (P1-11, matching Marzban's node-log convention):
// fetched over the panel->agent proc_logs request/response; the agent keeps a ring buffer and writes nothing to disk.

// procLogsTimeout is the timeout for a single log fetch.
const procLogsTimeout = 5 * time.Second

// nodeProcLogs fetches a node's process runtime logs (GET /api/nodes/:id/logs?proc=xray&limit=200).
func (h *Handler) nodeProcLogs(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	proc := c.Query("proc")
	if proc == "" {
		fail(c, http.StatusBadRequest, errors.New("proc is required"))
		return
	}
	limit := 200
	if v := c.Query("limit"); v != "" {
		m, err := strconv.Atoi(v)
		if err != nil || m < 1 || m > 1000 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-1000"))
			return
		}
		limit = m
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("logs-%d-%d", n.ID, time.Now().UnixNano()),
		agentproto.MsgProcLogs,
		agentproto.ProcLogsReq{Proc: proc, Limit: limit},
	)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	reply, err := h.hub.Request(int64(n.ID), env, procLogsTimeout)
	if err != nil {
		switch {
		case errors.Is(err, agenthub.ErrOffline):
			fail(c, http.StatusBadGateway, err)
		case errors.Is(err, agenthub.ErrTimeout):
			fail(c, http.StatusGatewayTimeout, err)
		default:
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	var ack agentproto.ProcLogsAck
	if err := reply.Decode(&ack); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if ack.Error != "" {
		fail(c, http.StatusBadRequest, errors.New(ack.Error))
		return
	}
	c.JSON(http.StatusOK, gin.H{"node_id": n.ID, "proc": ack.Proc, "lines": ack.Lines})
}
