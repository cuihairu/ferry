package agentproto

import "time"

// 节点角色取值（池归属与组件装配）。
const (
	RoleEntry   = "entry"   // 入口节点
	RoleLanding = "landing" // 落地节点
	RoleBoth    = "both"    // 入口与落地一体
)

// 拓扑方向取值（出海线/回国线）。
const (
	DirectionOut  = "out"  // 出海：国内入口 → 海外落地
	DirectionIn   = "in"   // 回国：海外入口 → 国内落地
	DirectionBoth = "both" // 双向
)

// NodeMeta 是节点注册元数据：配置文件填写为初值，注册时随 hello 上报，
// 面板可改且以面板为准（改后经 hello_ack 同步回 agent）。
// region 与 isp 必填，可标「未知」但不许空——区域与运营商是故障聚合维度。
type NodeMeta struct {
	Role                string   `json:"role"`                            // entry/landing/both
	Direction           string   `json:"direction"`                       // out/in/both
	LineType            string   `json:"line_type"`                       // cn2_gia/cu_vip/cmi/iplc/163/普通
	Region              string   `json:"region"`                          // 区域，可「未知」
	City                string   `json:"city,omitempty"`                  // 城市
	Datacenter          string   `json:"datacenter,omitempty"`            // 机房
	ISP                 string   `json:"isp"`                             // 运营商，可「未知」但不许空
	Labels              []string `json:"labels,omitempty"`                // 自定义标签
	Transport           string   `json:"transport"`                       // tls/quic/ws-tls/ssh
	BillingType         string   `json:"billing_type"`                    // 按流量/包月/固定带宽
	TrafficPriceCents   int64    `json:"traffic_price_cents,omitempty"`   // 流量单价（分/GB），仅按流量计费有意义
	MonthlyCostCents    int64    `json:"monthly_cost_cents,omitempty"`    // 月固定成本（分/月）
	Currency            string   `json:"currency,omitempty"`              // 币种，默认 CNY
	CostNote            string   `json:"cost_note,omitempty"`             // 成本备注
	BwUpMbps            int      `json:"bw_up_mbps"`                      // 套餐上行 Mbps，必填
	BwDownMbps          int      `json:"bw_down_mbps"`                    // 套餐下行 Mbps，必填
	MonthlyTrafficQuota int64    `json:"monthly_traffic_quota,omitempty"` // 月流量配额（字节，0=不限）
	RateLimited         bool     `json:"rate_limited"`                    // 是否限速
	Burst               bool     `json:"burst,omitempty"`                 // 是否峰值突发
}

// Hello 是 agent 连接后发出的第一条消息，认证失败面板直接断开。
type Hello struct {
	Token     string    `json:"token"`      // 节点令牌
	AgentID   string    `json:"agent_id"`   // 节点标识，对齐 nodes 表
	Version   string    `json:"version"`    // agent 构建版本
	Hostname  string    `json:"hostname"`   // 主机名，仅展示用
	StartedAt time.Time `json:"started_at"` // agent 进程启动时间
	Meta      NodeMeta  `json:"meta"`       // 节点注册元数据
}

// HelloAck 是面板的握手应答，Agent 据此校准心跳周期。
// Meta 为面板权威的节点元数据（Role/ISP 非空时覆盖 agent 本地初值）。
type HelloAck struct {
	ServerTime           time.Time `json:"server_time"`
	HeartbeatIntervalSec int       `json:"heartbeat_interval_sec"`
	Meta                 NodeMeta  `json:"meta"`
}

// Normalize 补齐元数据缺省值并把取值收敛到合法范围；
// region 与 isp 必填，未知时标「未知」（不许空）。面板与 agent 共用。
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

// Heartbeat 是周期心跳，汇总存活与负载快照。
// NetRxBytes/NetTxBytes 为开机以来累计字节，面板按心跳间隔差分得带宽速率。
type Heartbeat struct {
	UptimeSec     int64        `json:"uptime_sec"`
	Load1         float64      `json:"load1"`
	CPUUtil       float64      `json:"cpu_util"` // CPU 使用率 0-100
	MemUsedBytes  uint64       `json:"mem_used_bytes"`
	MemTotalBytes uint64       `json:"mem_total_bytes"`
	NetRxBytes    uint64       `json:"net_rx_bytes"` // 累计接收字节（不含 lo）
	NetTxBytes    uint64       `json:"net_tx_bytes"` // 累计发送字节（不含 lo）
	Conns         int          `json:"conns"`        // 本机 ESTABLISHED 连接数
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

// 探测目标类型取值。
const (
	ProbeTargetTunnel = "tunnel" // 经 relay 的隧道连通/延迟/丢包
	ProbeTargetExit   = "exit"   // 出口基线（区分隧道问题与本机问题）
	ProbeTargetPeer   = "peer"   // 入口互探（被封检测）
)

// 探测结论取值。
const (
	ProbeVerdictHealthy = "healthy"
	ProbeVerdictSick    = "sick"
)

// ProbeReport 是一条边缘探测结论。探测在节点本地完成，
// 面板只收结论做聚合判定，不集中探测。
type ProbeReport struct {
	TargetKind string    `json:"target_kind"`           // tunnel/exit/peer
	TargetNode int64     `json:"target_node,omitempty"` // 目标节点 ID（隧道/互探）
	TargetHost string    `json:"target_host,omitempty"` // 出口探测目标
	Direction  string    `json:"direction"`             // out/in，探测方向
	RttMs      int       `json:"rtt_ms,omitempty"`      // 往返延迟（毫秒）
	LossPct    int       `json:"loss_pct,omitempty"`    // 丢包率 0-100
	Reachable  bool      `json:"reachable"`             // 是否可达
	Blocked    bool      `json:"blocked"`               // 是否判定被封
	Verdict    string    `json:"verdict"`               // healthy/sick
	Region     string    `json:"region"`                // 目标区域快照（聚合用）
	ISP        string    `json:"isp"`                   // 目标运营商快照
	ProbedAt   time.Time `json:"probed_at"`
}

// ProbeReportBatch 一次批量上报多条探测结论。
type ProbeReportBatch struct {
	Items []ProbeReport `json:"items"`
}

// ProbeAck 确认面板已接收的条数。
type ProbeAck struct {
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
