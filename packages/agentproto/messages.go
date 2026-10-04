package agentproto

import "time"

// Hello 是 agent 连接后发出的第一条消息，认证失败面板直接断开。
type Hello struct {
	Token     string    `json:"token"`      // 节点令牌
	AgentID   string    `json:"agent_id"`   // 节点标识，对齐 nodes 表
	Version   string    `json:"version"`    // agent 构建版本
	Hostname  string    `json:"hostname"`   // 主机名，仅展示用
	StartedAt time.Time `json:"started_at"` // agent 进程启动时间
}

// HelloAck 是面板的握手应答，Agent 据此校准心跳周期。
type HelloAck struct {
	ServerTime           time.Time `json:"server_time"`
	HeartbeatIntervalSec int       `json:"heartbeat_interval_sec"`
}

// Heartbeat 是周期心跳，汇总存活与负载快照。
type Heartbeat struct {
	UptimeSec     int64        `json:"uptime_sec"`
	Load1         float64      `json:"load1"`
	MemUsedBytes  uint64       `json:"mem_used_bytes"`
	MemTotalBytes uint64       `json:"mem_total_bytes"`
	Certs         []CertStatus `json:"certs,omitempty"`
	Procs         []ProcStatus `json:"procs,omitempty"`
	At            time.Time    `json:"at"`
}

// HeartbeatAck 允许面板调整下次心跳间隔。
type HeartbeatAck struct {
	NextIntervalSec int `json:"next_interval_sec"`
}

// CertStatus 报告一张证书的到期时间，供面板提前告警。
type CertStatus struct {
	Domain   string    `json:"domain"`
	NotAfter time.Time `json:"not_after"`
}

// 进程状态机取值。
const (
	ProcRunning = "running"
	ProcStopped = "stopped"
	ProcCrashed = "crashed"
)

// ProcStatus 是单个被管进程的状态快照。
type ProcStatus struct {
	Name     string    `json:"name"`
	State    string    `json:"state"` // running/stopped/crashed
	Since    time.Time `json:"since"` // 进入当前状态的时间
	Restarts int       `json:"restarts"`
	PID      int       `json:"pid,omitempty"`
}

// 进程操作取值。
const (
	ProcActionStart  = "start"
	ProcActionStop   = "stop"
	ProcActionReload = "reload"
)

// ProcCtl 是面板下发的进程操作指令。
type ProcCtl struct {
	Proc   string `json:"proc"`
	Action string `json:"action"` // start/stop/reload
}

// ProcCtlAck 是进程操作的执行结果。
type ProcCtlAck struct {
	Proc   string `json:"proc"`
	Action string `json:"action"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// ProcReport 是 agent 主动上报的进程状态变化事件。
type ProcReport struct {
	Procs []ProcStatus `json:"procs"`
}

// ConfigPush 是面板下发的完整配置。
type ConfigPush struct {
	Proc    string `json:"proc"`    // 目标进程名（对齐 agent 进程规格）
	Kind    string `json:"kind"`    // xray/sing-box/hysteria2
	Version string `json:"version"` // 配置版本，单调递增或内容哈希
	Sha256  string `json:"sha256"`  // Payload 的十六进制 SHA-256
	Payload string `json:"payload"` // 完整配置文本
}

// ConfigAck 是配置下发的执行结果；Reverted 为真表示已回滚旧配置。
type ConfigAck struct {
	Proc      string `json:"proc"`
	Version   string `json:"version"`
	OK        bool   `json:"ok"`
	Reverted  bool   `json:"reverted,omitempty"`
	Validated bool   `json:"validated,omitempty"` // 是否通过了校验命令
	Error     string `json:"error,omitempty"`
}

// ProcTraffic 是单个进程自上次上报以来的流量增量与在线连接数。
type ProcTraffic struct {
	Proc  string    `json:"proc"`
	Rx    uint64    `json:"rx"` // 本次周期内的增量字节
	Tx    uint64    `json:"tx"`
	Conns int       `json:"conns"` // 采样时刻在线连接数
	At    time.Time `json:"at"`
}

// TrafficReport 一次批量上报多个进程的流量。
type TrafficReport struct {
	Items []ProcTraffic `json:"items"`
}

// TrafficAck 确认面板已记账的条数。
type TrafficAck struct {
	Recorded int `json:"recorded"`
}

// 告警级别与类别取值。
const (
	AlarmSeverityWarning  = "warning"
	AlarmSeverityCritical = "critical"

	AlarmKindProcCrash   = "proc_crash"   // 进程崩溃且拉起失败
	AlarmKindCertExpiry  = "cert_expiry"  // 证书临近到期
	AlarmKindHighLoad    = "high_load"    // 负载过高
	AlarmKindConfigError = "config_error" // 配置下发失败且回滚
)

// Alarm 是 agent 检测到的异常，面板侧应即时可见。
type Alarm struct {
	Kind     string    `json:"kind"`
	Severity string    `json:"severity"`
	Proc     string    `json:"proc,omitempty"`
	Message  string    `json:"message"`
	At       time.Time `json:"at"`
}
