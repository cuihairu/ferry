package storage

import "time"

// User 是一个订阅用户；SubToken 为订阅链接令牌。
type User struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Username   string     `gorm:"size:64;uniqueIndex" json:"username"`
	SubToken   string     `gorm:"size:64;uniqueIndex" json:"sub_token"`
	QuotaBytes int64      `gorm:"default:0" json:"quota_bytes"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Enabled    bool       `gorm:"default:true" json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
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
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
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

// CardBatch 是一批卡密的权益定义。
type CardBatch struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Name       string     `gorm:"size:128" json:"name"`
	GrantType  string     `gorm:"size:16" json:"grant_type"`   // add_quota / extend_days
	GrantValue int64      `gorm:"not null" json:"grant_value"` // 字节数或天数
	Total      int        `gorm:"not null" json:"total"`
	ExpiredAt  *time.Time `json:"expired_at"` // 卡密有效期，空为永久
	CreatedBy  string     `gorm:"size:64" json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	Codes      []CardCode `gorm:"foreignKey:BatchID;references:ID" json:"-"`
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
	ID             uint       `gorm:"primaryKey" json:"id"`
	EntryNodeID    *uint      `gorm:"index" json:"entry_node_id"`    // 入口节点（空=区域级分配）
	LandingNodeID  uint       `gorm:"index;not null" json:"landing_node_id"`
	Direction      string     `gorm:"size:8;not null" json:"direction"` // out/in，方向分流留痕
	Strategy       string     `gorm:"size:16;not null" json:"strategy"` // manual/least_conn/cost_first/perf_first/balanced
	Weight         int        `gorm:"default:0" json:"weight"`
	Reason         string     `gorm:"size:64" json:"reason"`           // region_fault/failover/manual...
	AssignedAt     time.Time  `gorm:"not null" json:"assigned_at"`
	ReleasedAt     *time.Time `json:"released_at"`                     // 空=生效中
	ReleaseReason  string     `gorm:"size:64" json:"release_reason"`
}

// ProbeReport 是一条边缘探测结论存证：探测在节点本地完成，
// 面板只收结论做聚合判定与历史回溯。
type ProbeReport struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	NodeID       uint       `gorm:"index;not null" json:"node_id"` // 探测者（入口 agent）
	TargetKind   string     `gorm:"size:16;not null;index:idx_probe_window,priority:1" json:"target_kind"` // tunnel/exit/peer
	TargetNodeID *uint      `gorm:"index:idx_probe_window,priority:2" json:"target_node_id"`
	TargetHost   string     `gorm:"size:255" json:"target_host"`
	Direction    string     `gorm:"size:8;not null;default:out" json:"direction"`
	RttMs        int        `gorm:"default:0" json:"rtt_ms"`
	LossPct      int        `gorm:"default:0" json:"loss_pct"` // 0-100
	Reachable    bool       `gorm:"default:false" json:"reachable"`
	Blocked      bool       `gorm:"default:false" json:"blocked"`
	Verdict      string     `gorm:"size:16;not null" json:"verdict"` // healthy/sick
	Region       string     `gorm:"size:64;not null;default:未知" json:"region"` // 目标区域快照
	ISP          string     `gorm:"size:32;not null;default:未知" json:"isp"`    // 目标运营商快照
	ProbedAt     time.Time  `gorm:"not null;index:idx_probe_window,priority:3" json:"probed_at"`
	CreatedAt    time.Time  `json:"created_at"`
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
	CreatedAt   time.Time  `json:"created_at"`
	PaidAt      *time.Time `json:"paid_at"`
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
