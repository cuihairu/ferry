package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
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
	switch delivered, errMsg := h.deliverProbeTasks(uint(id), task); delivered {
	case "ok":
		out["delivered"] = true
	case "rejected":
		out["error"] = errMsg
		c.JSON(http.StatusBadGateway, out)
		return
	default: // offline (desired state stored) or transport trouble: not a failed set
		out["error"] = errMsg
	}
	c.JSON(http.StatusOK, out)
}

// deliverProbeTasks pushes a validated task set to an online agent and returns
// one of: "ok" (acked), "offline" (not connected; the set stays desired state
// and is applied at the next hello), "transport" (delivery trouble; the set
// stays desired state either way), "rejected" (the agent refused the set).
func (h *Handler) deliverProbeTasks(nodeID uint, task agentproto.ProbeTask) (string, string) {
	if !h.hub.IsOnline(int64(nodeID)) {
		return "offline", ""
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("probetask-%d-%d", nodeID, time.Now().UnixNano()),
		agentproto.MsgProbeTask, task,
	)
	if err != nil {
		return "transport", err.Error()
	}
	reply, err := h.hub.Request(int64(nodeID), env, probeTaskAckTimeout)
	if err != nil {
		log.Printf("probe task delivery node=%d: %v", nodeID, err)
		return "transport", err.Error()
	}
	var ack agentproto.ProbeTaskAck
	if err := reply.Decode(&ack); err != nil || !ack.OK {
		if err == nil && ack.Error != "" {
			return "rejected", ack.Error
		}
		return "rejected", "agent rejected the probe task set"
	}
	return "ok", ""
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

// Probe-task names generated from paths carry this prefix; a sync rebuilds
// exactly this namespace and leaves every operator-authored task alone.
const pathTaskPrefix = "path-"

// importanceProbeInterval maps a path's probe importance (F-0924) to the
// dispatched tunnel-probe interval: business-critical paths are watched every
// minute, normal every five, low-value ones every quarter hour.
func importanceProbeInterval(imp string) int {
	switch imp {
	case "critical":
		return 60
	case "low":
		return 900
	default:
		return 300
	}
}

// syncProbeTasksFromPaths rebuilds the dispatched tunnel probes from the path
// table (POST /api/probe-tasks/sync-from-paths, F-0924): every enabled path
// becomes one `path-<id>` tunnel task on its entry node at the
// importance-derived interval, replacing the path namespace of previous syncs;
// operator-authored tasks (any other name) are left untouched, and paths that
// disappeared or were disabled simply stop being regenerated.
func (h *Handler) syncProbeTasksFromPaths(c *gin.Context) {
	var paths []storage.Path
	if err := h.db.Where("enabled = ?", true).Find(&paths).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var landings []storage.Node
	if err := h.db.Find(&landings).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	addrByID := map[uint]string{}
	for _, n := range landings {
		addrByID[n.ID] = net.JoinHostPort(n.Address, strconv.Itoa(n.Port))
	}

	// the desired path-<id> set per entry node
	pathSpecs := map[uint][]agentproto.ProbeTaskSpec{}
	for _, p := range paths {
		addr := addrByID[p.LandingNodeID]
		if addr == ":" || strings.HasPrefix(addr, ":") {
			continue // landing without an address cannot be probed
		}
		pathSpecs[p.EntryNodeID] = append(pathSpecs[p.EntryNodeID], agentproto.ProbeTaskSpec{
			Name:        fmt.Sprintf("%s%d", pathTaskPrefix, p.ID),
			TargetKind:  agentproto.ProbeTargetTunnel,
			Target:      addr,
			Direction:   p.Direction,
			IntervalSec: importanceProbeInterval(p.ProbeImportance),
		})
	}

	// every entry node of a path, plus nodes still carrying stale path-* tasks
	entries := map[uint]bool{}
	for nodeID := range pathSpecs {
		entries[nodeID] = true
	}
	var nodes []storage.Node
	if err := h.db.Where("probe_tasks LIKE ?", `%"path-%`).Find(&nodes).Error; err == nil {
		for _, n := range nodes {
			entries[n.ID] = true
		}
	}

	out := gin.H{"nodes": []gin.H{}}
	for nodeID := range entries {
		var n storage.Node
		if err := h.db.First(&n, nodeID).Error; err != nil {
			continue
		}
		// keep the operator's tasks, drop the previous path-* generation
		var cur agentproto.ProbeTask
		kept := []agentproto.ProbeTaskSpec{}
		if n.ProbeTasks != "" {
			if json.Unmarshal([]byte(n.ProbeTasks), &cur) == nil {
				for _, s := range cur.Specs {
					if !strings.HasPrefix(s.Name, pathTaskPrefix) {
						kept = append(kept, s)
					}
				}
			}
		}
		kept = append(kept, pathSpecs[nodeID]...)
		task, err := validateProbeTasks(probeTaskInput{Specs: kept})
		if err != nil {
			continue // cannot happen for generated+persisted specs; skip defensively
		}
		raw := ""
		if len(task.Specs) > 0 {
			if b, err := json.Marshal(task); err == nil {
				raw = string(b)
			}
		}
		if err := h.db.Model(&storage.Node{}).Where("id=?", nodeID).Update("probe_tasks", raw).Error; err != nil {
			out["nodes"] = append(out["nodes"].([]gin.H), gin.H{"id": nodeID, "error": err.Error()})
			continue
		}
		row := gin.H{"id": nodeID, "tasks": len(task.Specs), "delivered": false, "error": ""}
		if len(task.Specs) > 0 {
			switch state, msg := h.deliverProbeTasks(nodeID, task); state {
			case "ok":
				row["delivered"] = true
			case "rejected":
				row["error"] = msg
			case "transport":
				row["error"] = msg
			}
		}
		out["nodes"] = append(out["nodes"].([]gin.H), row)
	}
	c.JSON(http.StatusOK, out)
}
