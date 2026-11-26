package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/egresshealth"
	"github.com/cuihairu/ferry/server/internal/nodehealth"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Node operational state aggregation for the dash detail surface (F-0750):
// agent liveness, relay process state and the latest edge probe verdict in
// one call. Absent relay/probe history stays null — never invented.

// nodeAgentStatus is the agent liveness half of the aggregate: the node row
// itself carries the heartbeat-derived status (online/offline/unknown) and the
// last heartbeat time.
type nodeAgentStatus struct {
	Status      string     `json:"status"`
	LastSeen    *time.Time `json:"last_seen,omitempty"`
	Enabled     bool       `json:"enabled"`
	Maintenance bool       `json:"maintenance"`
}

// nodeRelayStatus is the relay process half: the "relay" managed process row
// (saved from heartbeat snapshots), plus the node role and pool state for
// context on how the node participates in paths.
type nodeRelayStatus struct {
	State     string    `json:"state"`
	PID       int       `json:"pid,omitempty"`
	Restarts  int       `json:"restarts"`
	UpdatedAt time.Time `json:"updated_at"`
	Role      string    `json:"role"`
	PoolState string    `json:"pool_state"`
	// Version is the reported build of the relay process (F-0816); empty until an
	// agent with version support reports it.
	Version string `json:"version,omitempty"`
}

// nodeRelayCaps is the structured relay capability declaration (F-0804) the agent
// last reported with a heartbeat; nil when the agent never reported one.
type nodeRelayCaps struct {
	TCPForward     bool `json:"tcp_forward"`
	UDPForward     bool `json:"udp_forward"`
	Reconnect      bool `json:"reconnect"`
	IdleCleanup    bool `json:"idle_cleanup"`
	Metrics        bool `json:"metrics"`
	StreamLimit    bool `json:"stream_limit"`
	BandwidthLimit bool `json:"bandwidth_limit"`
	MaxConns       int  `json:"max_conns"`
	MaxStreams     int  `json:"max_streams"`
	RateLimitKbps  int  `json:"rate_limit_kbps"`
}

// nodeProbeStatus is the probe half: the newest verdict this node produced as
// a prober. The verdict/rtt/loss come straight from the report row.
type nodeProbeStatus struct {
	TargetKind string    `json:"target_kind"`
	Direction  string    `json:"direction"`
	RttMs      int       `json:"rtt_ms"`
	JitterMs   int       `json:"jitter_ms"`
	LossPct    int       `json:"loss_pct"`
	Reachable  bool      `json:"reachable"`
	Verdict    string    `json:"verdict"`
	ProbedAt   time.Time `json:"probed_at"`
}

// nodeStatus is the aggregate payload of GET /api/nodes/:id/status.
type nodeStatus struct {
	Agent nodeAgentStatus  `json:"agent"`
	Relay *nodeRelayStatus `json:"relay"`
	Probe *nodeProbeStatus `json:"probe"`
	// Health is the node's own derived health (F-0902), distinct from the
	// per-path health pathhealth reports.
	Health string `json:"health"`
	// Egress is the node's own public-egress health (F-0905) from its exit
	// baseline probes -- "this node can reach the outside", as opposed to
	// Health's "others can reach this node".
	Egress    string         `json:"egress"`
	RelayCaps *nodeRelayCaps `json:"relay_caps,omitempty"`
}

// nodeStatus returns the agent/relay/probe state of one node (F-0750).
func (h *Handler) nodeStatus(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	out := nodeStatus{Agent: nodeAgentStatus{
		Status:      n.Status,
		LastSeen:    n.LastSeen,
		Enabled:     n.Enabled,
		Maintenance: n.Maintenance,
	}}
	// Node health (F-0902) derives from the node's own signals; computed on
	// read so it is always as fresh as the underlying state.
	out.Health = nodehealth.Evaluate(h.db, []storage.Node{*n}, time.Now())[n.ID]
	out.Egress = egresshealth.Evaluate(h.db, []storage.Node{*n}, time.Now())[n.ID]
	if n.RelayCaps != "" {
		var caps nodeRelayCaps
		if err := json.Unmarshal([]byte(n.RelayCaps), &caps); err == nil {
			out.RelayCaps = &caps
		}
	}

	var proc storage.NodeProcStatus
	err = h.db.Where("node_id=? AND proc=?", n.ID, "relay").First(&proc).Error
	if err == nil {
		out.Relay = &nodeRelayStatus{
			State:     proc.State,
			PID:       proc.PID,
			Restarts:  proc.Restarts,
			UpdatedAt: proc.UpdatedAt,
			Role:      n.Role,
			PoolState: n.PoolState,
			Version:   proc.Version,
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	var rep storage.ProbeReport
	err = h.db.Where("node_id=?", n.ID).Order("probed_at DESC, id DESC").First(&rep).Error
	if err == nil {
		out.Probe = &nodeProbeStatus{
			TargetKind: rep.TargetKind,
			Direction:  rep.Direction,
			RttMs:      rep.RttMs,
			JitterMs:   rep.JitterMs,
			LossPct:    rep.LossPct,
			Reachable:  rep.Reachable,
			Verdict:    rep.Verdict,
			ProbedAt:   rep.ProbedAt,
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// listProbeReports returns the edge probe verdict records (newest first, 200 by
// default). node_id filters to the reports produced by one prober.
func (h *Handler) listProbeReports(c *gin.Context) {
	limit := 200
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-1000"))
			return
		}
		limit = n
	}
	q := h.db.Model(&storage.ProbeReport{})
	if v := c.Query("node_id"); v != "" {
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil || id == 0 {
			fail(c, http.StatusBadRequest, errors.New("node_id must be a node id"))
			return
		}
		q = q.Where("node_id = ?", id)
	}
	out := []storage.ProbeReport{}
	if err := q.Order("probed_at DESC, id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
