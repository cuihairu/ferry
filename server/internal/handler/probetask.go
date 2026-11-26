package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// F-0921: probe task dispatch. The panel holds one desired dispatched set per
// node (Node.ProbeTasks), sends the full set on every agent hello and re-sends
// it here when the operator changes it. The agent merges dispatched tasks over
// its config-file probes and hot-swaps its probe runner; an empty set withdraws
// everything dispatched.

// probeTaskAckTimeout bounds the agent's probe_task_ack; applying a probe set
// is local validation plus persistence, so a short budget suffices.
const probeTaskAckTimeout = 10 * time.Second

// probeTaskInput is the PUT /api/nodes/:id/probe_tasks payload; an empty or
// absent specs list withdraws all dispatched tasks.
type probeTaskInput struct {
	Specs []agentproto.ProbeTaskSpec `json:"specs"`
}

// validateProbeTasks enforces the agent's own probe rules (agent config
// ValidateProbeSpecs) on the panel side: the agent is a separate module, so the
// rules are mirrored here rather than imported -- a dispatch the agent would
// reject is rejected before it costs a round trip.
func validateProbeTasks(in probeTaskInput) (agentproto.ProbeTask, error) {
	names := map[string]bool{}
	for i := range in.Specs {
		s := &in.Specs[i]
		if s.Name == "" || s.Target == "" {
			return agentproto.ProbeTask{}, fmt.Errorf("specs[%d].name and specs[%d].target are required", i, i)
		}
		if names[s.Name] {
			return agentproto.ProbeTask{}, fmt.Errorf("duplicate probe name %q", s.Name)
		}
		names[s.Name] = true
		if s.Direction == "" {
			s.Direction = agentproto.DirectionOut
		}
		switch s.TargetKind {
		case agentproto.ProbeTargetTunnel, agentproto.ProbeTargetExit, agentproto.ProbeTargetPeer:
		default:
			return agentproto.ProbeTask{}, fmt.Errorf("specs[%d].target_kind must be tunnel/exit/peer", i)
		}
		// An unset interval gets the same default the panel-side baseline uses;
		// anything explicit must stay in the agent's accepted range.
		if s.IntervalSec == 0 {
			s.IntervalSec = 60
		}
		if s.IntervalSec < 5 || s.IntervalSec > 3600 {
			return agentproto.ProbeTask{}, fmt.Errorf("specs[%d].interval_sec must be 5-3600", i)
		}
		switch s.Proto {
		case "", "tcp", "http", "https":
			if s.PayloadHex != "" || s.ExpectHex != "" {
				return agentproto.ProbeTask{}, fmt.Errorf("specs[%d].payload_hex/expect_hex are udp-only", i)
			}
			if s.HTTPPath != "" && !strings.HasPrefix(s.HTTPPath, "/") {
				return agentproto.ProbeTask{}, fmt.Errorf("specs[%d].http_path must start with /", i)
			}
			if s.ExpectStatus != 0 && (s.ExpectStatus < 100 || s.ExpectStatus > 599) {
				return agentproto.ProbeTask{}, fmt.Errorf("specs[%d].expect_status must be 100-599", i)
			}
			if s.TimeoutSec != 0 && (s.TimeoutSec < 1 || s.TimeoutSec > 60) {
				return agentproto.ProbeTask{}, fmt.Errorf("specs[%d].timeout_sec must be 1-60", i)
			}
		case "udp":
			if s.PayloadHex == "" {
				return agentproto.ProbeTask{}, fmt.Errorf("specs[%d].payload_hex is required for udp probes", i)
			}
		default:
			return agentproto.ProbeTask{}, fmt.Errorf("specs[%d].proto must be tcp/udp/http/https", i)
		}
	}
	// The version is the content hash of the validated set: identical sets share
	// a version, so re-sending the same list stays idempotent on the agent.
	raw, err := json.Marshal(in.Specs)
	if err != nil {
		return agentproto.ProbeTask{}, err
	}
	sum := sha256.Sum256(raw)
	task := agentproto.ProbeTask{Version: hex.EncodeToString(sum[:8]), Specs: in.Specs}
	return task, nil
}

// setProbeTasks sets a node's dispatched probe task set (PUT /api/nodes/:id/probe_tasks).
// The set persists regardless of the node's online state; an online node gets
// the dispatch applied right away, an offline one on its next hello.
func (h *Handler) setProbeTasks(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	var in probeTaskInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(in.Specs) > 100 {
		fail(c, http.StatusBadRequest, errors.New("at most 100 probe tasks per node"))
		return
	}
	var n storage.Node
	if err := h.db.First(&n, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	task, err := validateProbeTasks(in)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	raw := ""
	if len(task.Specs) > 0 {
		b, err := json.Marshal(task)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		raw = string(b)
	}
	if err := h.db.Model(&storage.Node{}).Where("id=?", id).Update("probe_tasks", raw).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	out := gin.H{"version": task.Version, "applied": len(task.Specs), "delivered": false, "error": ""}
	if len(task.Specs) == 0 {
		c.JSON(http.StatusOK, out) // withdrawal: nothing to deliver
		return
	}
	if !h.hub.IsOnline(int64(id)) {
		c.JSON(http.StatusOK, out) // desired state stored; applied at the next hello
		return
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("probetask-%d-%d", id, time.Now().UnixNano()),
		agentproto.MsgProbeTask, task,
	)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	reply, err := h.hub.Request(int64(id), env, probeTaskAckTimeout)
	if err != nil {
		// The set is persisted either way; a flaky delivery is not a failed set.
		log.Printf("probe task delivery node=%d: %v", id, err)
		out["error"] = err.Error()
		c.JSON(http.StatusOK, out)
		return
	}
	var ack agentproto.ProbeTaskAck
	if err := reply.Decode(&ack); err != nil || !ack.OK {
		msg := "agent rejected the probe task set"
		if err == nil && ack.Error != "" {
			msg = ack.Error
		}
		out["error"] = msg
		c.JSON(http.StatusBadGateway, out)
		return
	}
	out["delivered"] = true
	c.JSON(http.StatusOK, out)
}

// dispatchProbeTasks sends the node's desired probe task set after hello
// (F-0921); best-effort -- a failure just logs, the next hello retries.
func (h *Handler) dispatchProbeTasks(nodeID int64, hc *agenthub.Conn) {
	var n storage.Node
	if err := h.db.First(&n, nodeID).Error; err != nil || n.ProbeTasks == "" {
		return
	}
	var task agentproto.ProbeTask
	if err := json.Unmarshal([]byte(n.ProbeTasks), &task); err != nil || len(task.Specs) == 0 {
		return
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("probetask-hello-%d-%d", nodeID, time.Now().UnixNano()),
		agentproto.MsgProbeTask, task,
	)
	if err != nil {
		return
	}
	if _, err := h.hub.Request(nodeID, env, probeTaskAckTimeout); err != nil {
		log.Printf("probe task dispatch node=%d: %v", nodeID, err)
	}
}
