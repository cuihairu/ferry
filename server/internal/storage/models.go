package storage

import "time"

// User 是一个订阅用户；SubToken 为订阅链接令牌。
type User struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	Username    string     `gorm:"size:64;uniqueIndex" json:"username"`
	SubToken    string     `gorm:"size:64;uniqueIndex" json:"sub_token"`
	QuotaBytes  int64      `gorm:"default:0" json:"quota_bytes"`
	ResetCycle  string     `gorm:"size:8;default:none" json:"reset_cycle"` // 流量重置周期（P1-4）：none/day/week/month
	ExpiresAt   *time.Time `json:"expires_at"`
	Enabled     bool       `gorm:"default:true" json:"enabled"`
	IsAdmin     bool       `gorm:"default:false" json:"is_admin"`
	Password    string     `gorm:"size:128" json:"-"`
	ApiToken    string     `gorm:"size:64" json:"api_token"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Node 是一台代理节点。Token 供 agent 出站连接认证。
type Node struct {
	ID       uint       `gorm:"primaryKey" json:"id"`
	Name     string     `gorm:"size:64" json:"name"`
	Address  string     `gorm:"size:255" json:"address"`
	Port     int        `gorm:"not null" json:"port"`
	Protocol string     `gorm:"size:16" json:"protocol"` // vless/vmess/trojan/shadowsocks
	Config   string     `gorm:"type:text" json:"config"` // 协议配置模板 JSON
	Enabled  bool       `gorm:"default:true" json:"enabled"`
	Token    string     `gorm:"size:64;uniqueIndex" json:"token"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
	Status   string     `gorm:"size:16;default:unknown" json:"status"` // online/offline/unknown
	// 注册元数据（入口与负载均衡设计 §C.3/C.6）：agent 注册上报初值，
	// 面板可改且以面板为准（meta_init 置位后注册不再覆盖）。
	Role                     string     `gorm:"size:16;default:landing" json:"role"` // entry/landing/both
	Direction                string     `gorm:"size:8;default:out" json:"direction"` // out/in/both
	LineType                 string     `gorm:"size:16;default:普通" json:"line_type"` // cn2_gia/cu_vip/cmi/iplc/163/普通
	Region                   string     `gorm:"size:64;default:未知" json:"region"`    // 区域，故障聚合维度
	City                     string     `gorm:"size:64" json:"city"`
	Datacenter               string     `gorm:"size:128" json:"datacenter"`
	ISP                      string     `gorm:"size:32;default:未知" json:"isp"`          // 运营商，故障聚合维度
	Labels                   string     `gorm:"type:text" json:"labels"`                // JSON 数组
	Transport                string     `gorm:"size:16;default:tls" json:"transport"`   // tls/quic/ws-tls/ssh
	BillingType              string     `gorm:"size:16;default:包月" json:"billing_type"` // 按流量/包月/固定带宽
	TrafficPriceCents        int64      `gorm:"default:0" json:"traffic_price_cents"`   // 流量单价（分/GB）
	MonthlyCostCents         int64      `gorm:"default:0" json:"monthly_cost_cents"`    // 月固定成本（分/月）
	Currency                 string     `gorm:"size:8;default:CNY" json:"currency"`
	CostNote                 string     `gorm:"size:255" json:"cost_note"`
	BwUpMbps                 int        `gorm:"default:0" json:"bw_up_mbps"`                  // 套餐上行 Mbps
	BwDownMbps               int        `gorm:"default:0" json:"bw_down_mbps"`                // 套餐下行 Mbps
	MonthlyTrafficQuotaBytes int64      `gorm:"default:0" json:"monthly_traffic_quota_bytes"` // 月流量配额（字节，0=不限）
	RateLimited              bool       `gorm:"default:true" json:"rate_limited"`
	Burst                    bool       `gorm:"default:false" json:"burst"`
	SpeedMeasuredMbps        int        `gorm:"default:0" json:"speed_measured_mbps"` // 测速校准实测容量
	SpeedCalibratedAt        *time.Time `json:"speed_calibrated_at"`
	MetaInit                 bool       `gorm:"default:false" json:"-"` // 元数据是否已初始化（面板已接管）
	AgentVersion             string     `gorm:"size:64;default:''" json:"agent_version"` // agent 自报版本（A-23）
	// 入口池摘挂状态（E-16）：suspended=连续 sick 摘除，订阅入口池即时剔除；
	// 不动 enabled（agent 连接与探测保持，复位判定才有依据）。
	PoolState     string     `gorm:"size:16;default:active;index" json:"pool_state"`
	PoolChangedAt *time.Time `json:"pool_changed_at"`
	PoolReason    string     `gorm:"size:64" json:"pool_reason"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// TrafficLog 是一条流量记账记录（按用户按节点按周期汇总）。
type TrafficLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     uint      `gorm:"index:idx_traffic_logs_user,priority:1;not null" json:"user_id"`
	NodeID     *uint     `json:"node_id"`
	RxBytes    int64     `gorm:"default:0" json:"rx_bytes"`
	TxBytes    int64     `gorm:"default:0" json:"tx_bytes"`
	RecordedAt time.Time `gorm:"index:idx_traffic_logs_user,priority:2" json:"recorded_at"`
	User       User      `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
	Node       *Node     `gorm:"foreignKey:NodeID;references:ID;constraint:OnDelete:SET NULL" json:"-"`
}

// Setting 是面板设置键值（轻量 KV，Value 存 JSON 文本）。
type Setting struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CardBatch 是一批卡密的权益定义。
type CardBatch struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Name       string     `gorm:"size:128" json:"name"`
	GrantType  string     `gorm:"size:16" json:"grant_type"`   // add_quota / extend_days
	GrantValue int64      `gorm:"not null" json:"grant_value"` // 字节数或天数
	PriceCents int64      `gorm:"default:0" json:"price_cents"` // 在线售价（分），0=仅兑换不出售
	Total      int        `gorm:"not null" json:"total"`
	ExpiredAt  *time.Time `json:"expired_at"` // 卡密有效期，空为永久
	CreatedBy  string     `gorm:"size:64" json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	// 删批次连带删卡密（《支付设计》§3.1），导出发放前误建批次可整体回收。
	Codes []CardCode `gorm:"foreignKey:BatchID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}

// CardCode 是一张卡密；Code 明文存储以便导出发放。
type CardCode struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	BatchID   uint       `gorm:"index;not null" json:"batch_id"`
	Code      string     `gorm:"size:32;uniqueIndex" json:"code"`
	Status    string     `gorm:"size:16;default:unused" json:"status"` // unused/used/disabled
	FailCount int        `gorm:"default:0" json:"fail_count"`
	UsedBy    *uint      `json:"used_by"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// LandingAssignment 是一条落地分配记录（入口→落地的映射与策略留痕）。
// 入口池/落地池即 nodes.role 分组，不另建池表。
type LandingAssignment struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	EntryNodeID   *uint      `gorm:"index" json:"entry_node_id"` // 入口节点（空=区域级分配）
	Region        string     `gorm:"size:64" json:"region"`      // 区域级分配的区域名（入口级分配留空）
	LandingNodeID uint       `gorm:"index;not null" json:"landing_node_id"`
	Direction     string     `gorm:"size:8;not null" json:"direction"` // out/in，方向分流留痕
	Strategy      string     `gorm:"size:16;not null" json:"strategy"` // manual/least_conn/cost_first/perf_first/balanced
	Weight        int        `gorm:"default:0" json:"weight"`
	Reason        string     `gorm:"size:64" json:"reason"` // region_fault/failover/manual...
	AssignedAt    time.Time  `gorm:"not null" json:"assigned_at"`
	ReleasedAt    *time.Time `json:"released_at"` // 空=生效中
	ReleaseReason string     `gorm:"size:64" json:"release_reason"`
}

// ProbeReport 是一条边缘探测结论存证：探测在节点本地完成，
// 面板只收结论做聚合判定与历史回溯。
type ProbeReport struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	NodeID       uint      `gorm:"index;not null" json:"node_id"`                                         // 探测者（入口 agent）
	TargetKind   string    `gorm:"size:16;not null;index:idx_probe_window,priority:1" json:"target_kind"` // tunnel/exit/peer
	TargetNodeID *uint     `gorm:"index:idx_probe_window,priority:2" json:"target_node_id"`
	TargetHost   string    `gorm:"size:255" json:"target_host"`
	Direction    string    `gorm:"size:8;not null;default:out" json:"direction"`
	RttMs        int       `gorm:"default:0" json:"rtt_ms"`
	LossPct      int       `gorm:"default:0" json:"loss_pct"` // 0-100
	Reachable    bool      `gorm:"default:false" json:"reachable"`
	Blocked      bool      `gorm:"default:false" json:"blocked"`
	Verdict      string    `gorm:"size:16;not null" json:"verdict"`           // healthy/sick
	Region       string    `gorm:"size:64;not null;default:未知" json:"region"` // 目标区域快照
	ISP          string    `gorm:"size:32;not null;default:未知" json:"isp"`    // 目标运营商快照
	ProbedAt     time.Time `gorm:"not null;index:idx_probe_window,priority:3" json:"probed_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// DimensionStatus 是区域/运营商维度的状态灯数据：聚合判定的落库结果。
type DimensionStatus struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Scope     string    `gorm:"size:16;not null;uniqueIndex:idx_dimension_status" json:"scope"` // region/isp
	Key       string    `gorm:"size:64;not null;uniqueIndex:idx_dimension_status" json:"key"`   // 区域名或运营商名
	State     string    `gorm:"size:16;not null;default:healthy" json:"state"`                  // healthy/degraded/failed
	Reason    string    `gorm:"size:255" json:"reason"`
	Since     time.Time `gorm:"not null" json:"since"` // 进入当前状态的时间
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

// NodeConfig 是一次配置下发的记录与结果（A-18）：pending → applied/failed。
type NodeConfig struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	NodeID    uint      `gorm:"index;not null" json:"node_id"`
	Proc      string    `gorm:"size:64;not null" json:"proc"`
	Kind      string    `gorm:"size:32" json:"kind"`
	Version   string    `gorm:"size:64;not null" json:"version"` // 内容哈希，与 sha256 同值
	Sha256    string    `gorm:"size:64;not null" json:"sha256"`
	Payload   string    `gorm:"type:text" json:"payload"`
	Status    string    `gorm:"size:16;not null;default:pending" json:"status"` // pending/applied/failed
	Reverted  bool      `gorm:"default:false" json:"reverted"`
	Validated bool      `gorm:"default:false" json:"validated"`
	Error     string    `gorm:"size:512" json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Alert 是一条异常告警（A-22）：agent 上报的进程崩溃/证书临期/高负载/配置错误，
// 同节点同类别同进程只保留一条活跃告警（重复上报刷新消息），进程恢复自动消解。
type Alert struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	NodeID     uint       `gorm:"index;not null" json:"node_id"`
	Kind       string     `gorm:"size:32;not null;index" json:"kind"`             // proc_crash/cert_expiry/high_load/config_error
	Severity   string     `gorm:"size:16;not null" json:"severity"`               // warning/critical
	Proc       string     `gorm:"size:64" json:"proc,omitempty"`
	Message    string     `gorm:"type:text" json:"message"`
	State      string     `gorm:"size:16;not null;default:active;index" json:"state"` // active/resolved
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// NodeTrafficLog 是节点级进程流量记账（A-20）：agent 按进程周期上报的增量。
// 用户级记账走 traffic_logs（P0-9），需要逐用户统计映射（P1-3）。
type NodeTrafficLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	NodeID     uint      `gorm:"index:idx_node_traffic,priority:1;not null" json:"node_id"`
	Proc       string    `gorm:"size:64;not null" json:"proc"`
	RxBytes    int64     `gorm:"default:0" json:"rx_bytes"`
	TxBytes    int64     `gorm:"default:0" json:"tx_bytes"`
	Conns      int       `gorm:"default:0" json:"conns"`
	RecordedAt time.Time `gorm:"not null;index:idx_node_traffic,priority:2" json:"recorded_at"`
	CreatedAt  time.Time `json:"created_at"`
}

// PaymentOrder 是订单（三账之一：谁该收多少）。
type PaymentOrder struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	OrderNo     string     `gorm:"size:32;uniqueIndex" json:"order_no"`
	UserID      uint       `gorm:"index;not null" json:"user_id"`
	Provider    string     `gorm:"size:16" json:"provider"` // card / epusdt / wechat / alipay
	AmountCents int64      `gorm:"default:0" json:"amount_cents"`
	Product     string     `gorm:"size:128" json:"product"`
	Status      string     `gorm:"size:16;default:pending;index" json:"status"` // pending/paid/failed/expired
	// 到账自动发放口径（在线支付用，卡密走批次自带）：add_quota/extend_days，空=不自动发放。
	GrantType  string     `gorm:"size:16" json:"grant_type,omitempty"`
	GrantValue int64      `gorm:"default:0" json:"grant_value,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	PaidAt     *time.Time `json:"paid_at"`
	// 不设 User 关联：财务记录不随用户删除（无外键）。
}

// PaymentTransaction 是支付流水（三账之二：外部实际发生的收付）。
type PaymentTransaction struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	OrderNo     string    `gorm:"size:32;index" json:"order_no"`
	Provider    string    `gorm:"size:16;uniqueIndex:idx_provider_external" json:"provider"`
	ExternalID  string    `gorm:"size:128;uniqueIndex:idx_provider_external" json:"external_id"`
	AmountCents int64     `gorm:"not null" json:"amount_cents"`
	Direction   string    `gorm:"size:8;default:in" json:"direction"` // in/out
	Raw         string    `gorm:"type:text" json:"raw"`
	OccurredAt  time.Time `json:"occurred_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// Grant 是发放记录（三账之三：用户实际拿到的权益）。
type Grant struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	OrderNo    string    `gorm:"size:32;index" json:"order_no"`
	UserID     uint      `gorm:"index;not null" json:"user_id"`
	GrantType  string    `gorm:"size:16" json:"grant_type"`   // add_quota / extend_days
	GrantValue int64     `gorm:"not null" json:"grant_value"` // 字节数或天数
	Snapshot   string    `gorm:"type:text" json:"snapshot"`   // 变更前后快照 JSON
	CreatedAt  time.Time `json:"created_at"`
	// 不设 User 关联：发放记录不随用户删除（无外键）。
}

// Provider 是云提供商凭证（OS-1）：接入 OpenTofu 供给引擎的 provider
// 认证机密。AccessKey 为 AES-256-GCM 密文（secret 统一入口加密），
// 接口层永不回显明文，仅 server 执行供给时解密使用。
type Provider struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:64;not null" json:"name"`
	Type      string    `gorm:"size:32;not null" json:"type"`        // OpenTofu provider 名（vultr/hetzner/…）
	AccessKey string    `gorm:"size:512;not null" json:"access_key"` // 密文，接口层以 has_access_key 呈现
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProvisionTemplate 是机型模板（OS-1）：dash 录入，OS-2 渲染 HCL 供给，
// OS-4 入池时按模板补全节点元数据（方向/线路/区域/成本口径与节点对齐）。
type ProvisionTemplate struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	Name              string    `gorm:"size:64;not null" json:"name"`
	ProviderID        uint      `gorm:"index;not null" json:"provider_id"`
	Plan              string    `gorm:"size:64" json:"plan"`              // 机型 slug
	Region            string    `gorm:"size:64" json:"region"`            // 区域
	Image             string    `gorm:"size:64" json:"image"`             // 系统镜像（provider 各自口径）
	BwMbps            int       `json:"bw_mbps"`                          // 带宽（Mbps）
	BillingType       string    `gorm:"size:16" json:"billing_type"`      // 包月 / 按流量
	MonthlyCostCents  int64     `json:"monthly_cost_cents"`               // 月固定成本（分）
	TrafficPriceCents int64     `json:"traffic_price_cents"`              // 流量单价（分/GB）
	Direction         string    `gorm:"size:8;default:out" json:"direction"` // out/in/both
	LineType          string    `gorm:"size:32" json:"line_type"`         // 163/cn2_gia/cu_vip/cmi/iplc
	Role              string    `gorm:"size:16;default:entry" json:"role"`   // entry/landing/both/relay
	Transport         string    `gorm:"size:16" json:"transport"`         // tls/ws-tls/quic/ssh
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ProvisionJob 是一次供给执行留痕（OS-2）：plan/apply 受控执行的结果与
// 日志尾部。单实例不并发 apply；日志不含机密（密钥走 TF_VAR 环境变量）。
type ProvisionJob struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	TemplateID   uint      `gorm:"index;not null" json:"template_id"`
	TemplateName string    `gorm:"size:64" json:"template_name"`
	Action       string    `gorm:"size:8;not null" json:"action"` // plan / apply
	Status       string    `gorm:"size:8;not null" json:"status"` // running / ok / failed
	Log          string    `gorm:"type:text" json:"log"`
	CreatedAt    time.Time `json:"created_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}
