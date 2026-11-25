package agentproto

import "time"

// Node role values (pool membership and component assembly).
const (
	RoleEntry   = "entry"   // entry node
	RoleLanding = "landing" // landing node
	RoleBoth    = "both"    // entry and landing combined
)

// Topology direction values (overseas line / return line).
const (
	DirectionOut  = "out"  // outbound: domestic entry → overseas landing
	DirectionIn   = "in"   // return: overseas entry → domestic landing
	DirectionBoth = "both" // bidirectional
)

// NodeMeta is node registration metadata: the config file supplies the initial
// values, reported with hello at registration; the panel may change them and
// the panel wins (changes sync back to the agent via hello_ack).
// region and isp are required; they may be marked "unknown" but never empty —
// region and ISP are fault-aggregation dimensions.
type NodeMeta struct {
	Role                string   `json:"role"`                            // entry/landing/both
	Direction           string   `json:"direction"`                       // out/in/both
	LineType            string   `json:"line_type"`                       // cn2_gia/cu_vip/cmi/iplc/163/standard
	Region              string   `json:"region"`                          // region, may be "unknown"
	City                string   `json:"city,omitempty"`                  // city
	Datacenter          string   `json:"datacenter,omitempty"`            // datacenter
	ISP                 string   `json:"isp"`                             // ISP, may be "unknown" but must not be empty
	Labels              []string `json:"labels,omitempty"`                // custom labels
	Transport           string   `json:"transport"`                       // tls/quic/ws-tls/ssh
	BillingType         string   `json:"billing_type"`                    // usage-based/monthly/fixed-bandwidth
	TrafficPriceCents   int64    `json:"traffic_price_cents,omitempty"`   // traffic unit price (cents/GB), only meaningful for usage-based billing
	MonthlyCostCents    int64    `json:"monthly_cost_cents,omitempty"`    // fixed monthly cost (cents/month)
	Currency            string   `json:"currency,omitempty"`              // currency, defaults to CNY
	CostNote            string   `json:"cost_note,omitempty"`             // cost note
	BwUpMbps            int      `json:"bw_up_mbps"`                      // plan upload Mbps, required
	BwDownMbps          int      `json:"bw_down_mbps"`                    // plan download Mbps, required
	MonthlyTrafficQuota int64    `json:"monthly_traffic_quota,omitempty"` // monthly traffic quota (bytes, 0 = unlimited)
	RateLimited         bool     `json:"rate_limited"`                    // whether rate-limited
	Burst               bool     `json:"burst,omitempty"`                 // whether burst is allowed
}

// Hello is the first message the agent sends after connecting; on auth failure the panel drops it.
type Hello struct {
	Token     string    `json:"token"`      // node token
	AgentID   string    `json:"agent_id"`   // node identifier, aligned with the nodes table
	Version   string    `json:"version"`    // agent build version
	Hostname  string    `json:"hostname"`   // hostname, display only
	StartedAt time.Time `json:"started_at"` // agent process start time
	Meta      NodeMeta  `json:"meta"`       // node registration metadata
	// Caps is the agent's statically declared capability set (F-0228), derived
	// from its own runtime config (managed procs / probe tasks / relay spec).
	// The panel records it on first registration; its own stored list wins after.
	Caps []string `json:"caps,omitempty"`
}

// HelloAck is the panel's handshake reply; the agent calibrates its heartbeat period from it.
// Meta is the panel-authoritative node metadata (non-empty Role/ISP overrides the agent's local initial values).
type HelloAck struct {
	ServerTime           time.Time `json:"server_time"`
	HeartbeatIntervalSec int       `json:"heartbeat_interval_sec"`
	Meta                 NodeMeta  `json:"meta"`
}

// Normalize fills in metadata defaults and clamps values into the legal range;
// region and isp are required, marked "unknown" when not known (never empty). Shared by panel and agent.
func (m *NodeMeta) Normalize() {
	if m.Role != RoleEntry && m.Role != RoleLanding && m.Role != RoleBoth {
		m.Role = RoleLanding
	}
	if m.Direction != DirectionOut && m.Direction != DirectionIn && m.Direction != DirectionBoth {
		m.Direction = DirectionOut
	}
	switch m.LineType {
	case "cn2_gia", "cu_vip", "cmi", "iplc", "163":
	default:
		m.LineType = "普通"
	}
	if m.Region == "" {
		m.Region = "未知"
	}
	if m.ISP == "" {
		m.ISP = "未知"
	}
	switch m.Transport {
	case "tls", "quic", "ws-tls", "ssh":
	default:
		m.Transport = "tls"
	}
	switch m.BillingType {
	case "按流量", "包月", "固定带宽":
	default:
		m.BillingType = "包月"
	}
	if m.Currency == "" {
		m.Currency = "CNY"
	}
}

// Heartbeat is the periodic heartbeat, summarizing liveness and a load snapshot.
// NetRxBytes/NetTxBytes are cumulative bytes since boot; the panel differences them across heartbeat intervals to get bandwidth rates.
type Heartbeat struct {
	UptimeSec     int64        `json:"uptime_sec"`
	Load1         float64      `json:"load1"`
	CPUUtil       float64      `json:"cpu_util"` // CPU utilization 0-100
	MemUsedBytes  uint64       `json:"mem_used_bytes"`
	MemTotalBytes uint64       `json:"mem_total_bytes"`
	NetRxBytes    uint64       `json:"net_rx_bytes"` // cumulative received bytes (excluding lo)
	NetTxBytes    uint64       `json:"net_tx_bytes"` // cumulative sent bytes (excluding lo)
	Conns         int          `json:"conns"`        // local ESTABLISHED connection count
	Certs         []CertStatus `json:"certs,omitempty"`
	Procs         []ProcStatus `json:"procs,omitempty"`
	// Caps is the dynamic runtime capability state (F-0229): capability name ->
	// ok/degraded/off, derived from the live component states each heartbeat.
	// Absent keys mean "unknown" and never disqualify a node.
	Caps map[string]string `json:"caps,omitempty"`
	At   time.Time         `json:"at"`
}

// HeartbeatAck lets the panel adjust the next heartbeat interval.
type HeartbeatAck struct {
	NextIntervalSec int `json:"next_interval_sec"`
}

// CertStatus reports a certificate's expiry time so the panel can alarm early.
type CertStatus struct {
	Domain   string    `json:"domain"`
	NotAfter time.Time `json:"not_after"`
}

// Process state machine values.
const (
	ProcRunning = "running"
	ProcStopped = "stopped"
	ProcCrashed = "crashed"
)

// ProcStatus is a state snapshot of a single managed process.
type ProcStatus struct {
	Name     string            `json:"name"`
	State    string            `json:"state"` // running/stopped/crashed
	Since    time.Time         `json:"since"` // time it entered the current state
	Restarts int               `json:"restarts"`
	PID      int               `json:"pid,omitempty"`
	Metrics  map[string]uint64 `json:"metrics,omitempty"` // process metrics (key-value numeric map, collected from MetricsURL)
}

// Process action values.
const (
	ProcActionStart  = "start"
	ProcActionStop   = "stop"
	ProcActionReload = "reload"
)

// ProcCtl is a process control command issued by the panel.
type ProcCtl struct {
	Proc   string `json:"proc"`
	Action string `json:"action"` // start/stop/reload
}

// ProcCtlAck is the result of a process control action.
type ProcCtlAck struct {
	Proc   string `json:"proc"`
	Action string `json:"action"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// ProcLogsReq is the panel's request to fetch process runtime logs (P1-11).
type ProcLogsReq struct {
	Proc  string `json:"proc"`
	Limit int    `json:"limit"` // last N lines, default 200, max 1000
}

// ProcLogsAck is the process log reply; the agent side uses an in-memory ring buffer, emptied on restart.
type ProcLogsAck struct {
	Proc  string   `json:"proc"`
	Lines []string `json:"lines"`
	Error string   `json:"error,omitempty"`
}

// ProcReport is a process state-change event proactively reported by the agent.
type ProcReport struct {
	Procs []ProcStatus `json:"procs"`
}

// ConfigPush is the complete configuration pushed by the panel.
type ConfigPush struct {
	Proc    string `json:"proc"`    // target process name (aligned with the agent process spec)
	Kind    string `json:"kind"`    // xray/sing-box/hysteria2
	Version string `json:"version"` // config version, monotonically increasing or a content hash
	Sha256  string `json:"sha256"`  // hex SHA-256 of Payload
	Payload string `json:"payload"` // full config text
}

// ConfigAck is the result of a config push; Reverted true means the old config was rolled back.
type ConfigAck struct {
	Proc      string `json:"proc"`
	Version   string `json:"version"`
	OK        bool   `json:"ok"`
	Reverted  bool   `json:"reverted,omitempty"`
	Validated bool   `json:"validated,omitempty"` // whether it passed the validation command
	Error     string `json:"error,omitempty"`
}

// ProcTraffic is a single process's traffic delta since the last report plus its live connection count.
type ProcTraffic struct {
	Proc  string        `json:"proc"`
	Rx    uint64        `json:"rx"` // delta bytes within this period
	Tx    uint64        `json:"tx"`
	Conns int           `json:"conns"` // live connection count at sampling time
	At    time.Time     `json:"at"`
	Users []UserTraffic `json:"users,omitempty"` // per-user deltas (xray gRPC stats, P1-3)
	// DirectBytes/BlockedBytes are this period's diverted-outbound traffic deltas (SAVE-1 direct /
	// SAVE-4 blocking, both fetched in one xray outbound counter query); omitted when not collected.
	DirectBytes  uint64 `json:"direct_bytes,omitempty"`
	BlockedBytes uint64 `json:"blocked_bytes,omitempty"`
}

// UserTraffic is a single user's traffic delta over the collection period (xray stats collection).
// Email is the xray inbound user email; the panel maps it via users.username.
type UserTraffic struct {
	Email string `json:"email"`
	Rx    uint64 `json:"rx"` // user receive (xray downlink)
	Tx    uint64 `json:"tx"` // user send (xray uplink)
}

// TrafficReport reports traffic for multiple processes in one batch.
type TrafficReport struct {
	Items []ProcTraffic `json:"items"`
}

// TrafficAck confirms how many entries the panel has recorded.
type TrafficAck struct {
	Recorded int `json:"recorded"`
}

// Calibrate is the report of the lightweight speed test run after the agent registers (E-8): measured download capacity.
type Calibrate struct {
	MeasuredDownMbps int     `json:"measured_down_mbps"` // measured download Mbps
	Bytes            int64   `json:"bytes"`              // bytes measured
	Seconds          float64 `json:"seconds"`            // measurement duration (seconds)
}

// CalibrateAck is the panel's calibration reply: whether the measurement is accepted as effective capacity.
type CalibrateAck struct {
	Accepted          bool   `json:"accepted"`
	EffectiveDownMbps int    `json:"effective_down_mbps"` // effective capacity (accepted = measured, otherwise = plan)
	Note              string `json:"note,omitempty"`
}

// Probe target kind values.
const (
	ProbeTargetTunnel = "tunnel" // tunnel reachability/latency/loss via relay
	ProbeTargetExit   = "exit"   // exit baseline (distinguishes tunnel problems from local ones)
	ProbeTargetPeer   = "peer"   // entry-node cross-probing (block detection)
)

// Probe verdict values.
const (
	ProbeVerdictHealthy = "healthy"
	ProbeVerdictSick    = "sick"
)

// ProbeReport is a single edge-probe verdict. Probing happens locally on the node;
// the panel only collects verdicts for aggregation and judgment, it does not probe centrally.
type ProbeReport struct {
	TargetKind string    `json:"target_kind"`           // tunnel/exit/peer
	TargetNode int64     `json:"target_node,omitempty"` // target node ID (tunnel/peer)
	TargetHost string    `json:"target_host,omitempty"` // exit probe target
	Direction  string    `json:"direction"`             // out/in, probe direction
	RttMs      int       `json:"rtt_ms,omitempty"`      // round-trip latency (ms)
	LossPct    int       `json:"loss_pct,omitempty"`    // loss percentage 0-100
	Reachable  bool      `json:"reachable"`             // whether reachable
	Blocked    bool      `json:"blocked"`               // whether judged blocked
	Verdict    string    `json:"verdict"`               // healthy/sick
	Region     string    `json:"region"`                // target region snapshot (for aggregation)
	ISP        string    `json:"isp"`                   // target ISP snapshot
	ProbedAt   time.Time `json:"probed_at"`
}

// ProbeReportBatch reports multiple probe verdicts in one batch.
type ProbeReportBatch struct {
	Items []ProbeReport `json:"items"`
}

// ProbeAck confirms how many entries the panel has received.
type ProbeAck struct {
	Recorded int `json:"recorded"`
}

// Alarm severity and kind values.
const (
	AlarmSeverityWarning  = "warning"
	AlarmSeverityCritical = "critical"

	AlarmKindProcCrash   = "proc_crash"   // process crashed and failed to restart
	AlarmKindCertExpiry  = "cert_expiry"  // certificate nearing expiry
	AlarmKindHighLoad    = "high_load"    // load too high
	AlarmKindConfigError = "config_error" // config push failed and rolled back
)

// Alarm is an anomaly detected by the agent; it must be immediately visible on the panel side.
type Alarm struct {
	Kind     string    `json:"kind"`
	Severity string    `json:"severity"`
	Proc     string    `json:"proc,omitempty"`
	Message  string    `json:"message"`
	At       time.Time `json:"at"`
}

// Upgrade is the self-upgrade command issued by the panel (A-23): the agent downloads the new binary, replaces itself, and restarts.
type Upgrade struct {
	Version string `json:"version"` // target version
	URL     string `json:"url"`     // download URL of the new binary
	Sha256  string `json:"sha256"`  // checksum (hex, empty means no check)
}

// UpgradeAck is the immediate reply to the upgrade command: accepted or rejected (rejection carries a reason, no binary swap).
type UpgradeAck struct {
	Version string `json:"version"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// Self-upgrade progress states (F-0712), reported in flow order; applied means
// the binary was swapped and the restart is imminent.
const (
	UpgradeStateDownloading = "downloading"
	UpgradeStateApplying    = "applying"
	UpgradeStateApplied     = "applied"
	UpgradeStateFailed      = "failed"
)

// UpgradeStatus is the agent's one-way progress report during a self-upgrade
// (F-0712), keyed by the upgrade command id; failed carries the reason.
type UpgradeStatus struct {
	UpgradeID string `json:"upgrade_id"`
	Version   string `json:"version"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
}

// ValidUpgradeState reports whether s is a known upgrade progress state.
func ValidUpgradeState(s string) bool {
	switch s {
	case UpgradeStateDownloading, UpgradeStateApplying, UpgradeStateApplied, UpgradeStateFailed:
		return true
	}
	return false
}
