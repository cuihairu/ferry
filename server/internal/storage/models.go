package storage

import "time"

// User 是一个订阅用户；SubToken 为订阅链接令牌。
type User struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Username   string     `gorm:"size:64;uniqueIndex" json:"username"`
	SubToken   string     `gorm:"size:64;uniqueIndex" json:"sub_token"`
	QuotaBytes int64      `gorm:"default:0" json:"quota_bytes"`
	ResetCycle string     `gorm:"size:8;default:none" json:"reset_cycle"` // 流量重置周期（P1-4）：none/day/week/month
	ExpiresAt  *time.Time `json:"expires_at"`
	Enabled    bool       `gorm:"default:true" json:"enabled"`
	IsAdmin    bool       `gorm:"default:false" json:"is_admin"`
	Password   string     `gorm:"size:128" json:"-"`
	ApiToken   string     `gorm:"size:64" json:"api_token"`
	// 两步验证（安全设计 §1，P1）：TOTP 密钥经 secret_store 加密落库
	//（v1:nonce:ct 密文，主密钥不入库）；绑定后登录需 6 位 TOTP 或一次性
	// 恢复码，未绑定者登录行为不变。
	TOTPSecret  string `gorm:"size:256" json:"-"`
	TOTPEnabled bool   `gorm:"default:false" json:"-"`
	// 通知偏好（NT-2）：站内信通道内按类型开关；通道维度待第二通道（邮件/TG）落地再扩。
	NotifyExpiry       bool      `gorm:"default:true" json:"notify_expiry"`      // 到期提醒
	NotifyTraffic      bool      `gorm:"default:true" json:"notify_traffic"`     // 流量预警
	TrafficWarnPercent int       `gorm:"default:80" json:"traffic_warn_percent"` // 流量预警阈值（1-100）
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
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
	MetaInit                 bool       `gorm:"default:false" json:"-"`                  // 元数据是否已初始化（面板已接管）
	AgentVersion             string     `gorm:"size:64;default:''" json:"agent_version"` // agent 自报版本（A-23）
	// 入口池摘挂状态（E-16）：suspended=连续 sick 摘除，订阅入口池即时剔除；
	// 不动 enabled（agent 连接与探测保持，复位判定才有依据）。
	PoolState     string     `gorm:"size:16;default:active;index" json:"pool_state"`
	PoolChangedAt *time.Time `json:"pool_changed_at"`
	// 供给入池流水线（OS-4）：provisioning 节点的进度/失败原因注记，
	// 转 online 时清空；上下线不写此列。
	ProvisionNote string    `gorm:"size:255" json:"provision_note"`
	PoolReason    string    `gorm:"size:64" json:"pool_reason"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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
	GrantType  string     `gorm:"size:16" json:"grant_type"`    // add_quota / extend_days
	GrantValue int64      `gorm:"not null" json:"grant_value"`  // 字节数或天数
	PriceCents int64      `gorm:"default:0" json:"price_cents"` // 在线售价（分），0=仅兑换不出售
	Total      int        `gorm:"not null" json:"total"`
	ExpiredAt  *time.Time `json:"expired_at"` // 卡密有效期，空为永久
	// 代理归属（DS-1）：0=面板自营；>0=代理名下批次，兑换时带入订单与账目。
	DistributorID uint      `gorm:"default:0;index" json:"distributor_id"`
	CreatedBy     string    `gorm:"size:64" json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
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
	Kind       string     `gorm:"size:32;not null;index" json:"kind"` // proc_crash/cert_expiry/high_load/config_error
	Severity   string     `gorm:"size:16;not null" json:"severity"`   // warning/critical
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
	ID           uint      `gorm:"primaryKey" json:"id"`
	NodeID       uint      `gorm:"index:idx_node_traffic,priority:1;not null" json:"node_id"`
	Proc         string    `gorm:"size:64;not null" json:"proc"`
	RxBytes      int64     `gorm:"default:0" json:"rx_bytes"`
	TxBytes      int64     `gorm:"default:0" json:"tx_bytes"`
	DirectBytes  int64     `gorm:"default:0" json:"direct_bytes"`  // 直连分流出站字节（SAVE-1/7）
	BlockedBytes int64     `gorm:"default:0" json:"blocked_bytes"` // 被拦截出站字节（SAVE-4 广告/追踪拦截）
	Conns        int       `gorm:"default:0" json:"conns"`
	RecordedAt   time.Time `gorm:"not null;index:idx_node_traffic,priority:2" json:"recorded_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// NodeProcStatus 是被管进程的最近状态快照（SAVE-3）：心跳携带的 ProcStatus
// 逐节点逐进程 upsert，指标（nginx cache/Squid 命中统计等）JSON 落 metrics。
type NodeProcStatus struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	NodeID    uint      `gorm:"uniqueIndex:idx_node_proc;not null" json:"node_id"`
	Proc      string    `gorm:"size:64;not null;uniqueIndex:idx_node_proc" json:"proc"`
	State     string    `gorm:"size:16;not null" json:"state"` // running/stopped/crashed
	PID       int       `gorm:"column:pid;default:0" json:"pid,omitempty"`
	Restarts  int       `gorm:"default:0" json:"restarts"`
	Metrics   string    `gorm:"type:text" json:"metrics,omitempty"` // JSON 数字对象（key-value）
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

// UserContact 是用户联系信息绑定与触达偏好（触达批 TOUCH-1）：TG/邮箱至少
// 一个必填（panel 引导补全），stale 为投递失败待换绑标记（TOUCH-5 回执联动）。
// RoutineEmails 只管域名例行邮件的退订；账单类涉及权益默认必收，无开关。
type UserContact struct {
	UserID        uint      `gorm:"primaryKey" json:"user_id"`
	TgChatID      string    `gorm:"size:64" json:"tg_chat_id,omitempty"`
	Email         string    `gorm:"size:255" json:"email,omitempty"`
	RoutineEmails bool      `json:"routine_emails"` // 缺省 true 由 handler 置（gorm default 标签会吞 Create 零值）
	BoundAt       time.Time `json:"bound_at"`
	Stale         bool      `gorm:"default:false" json:"stale"`   // 失效待换绑
	FailStreak    int       `gorm:"default:0" json:"fail_streak"` // 连续投递失败计数（成功清零）
	UpdatedAt     time.Time `json:"updated_at"`
}

// TouchJob 是一条触达任务与结果（触达批 TOUCH-4）：例行触达先落任务再经
// Herald 投递（outbox 语义），event_id 关联 outbox 行；投出即 sent（ferry→
// Herald 腿 2xx），通道分发失败由 TOUCH-5 回执改写并联动 stale。
type TouchJob struct {
	ID        int64      `gorm:"primaryKey" json:"id"`
	UserID    int64      `gorm:"index;not null" json:"user_id"`
	Channel   string     `gorm:"size:16;not null" json:"channel"`                                               // email（ferry 不自建通道，经 Herald 分发）
	Kind      string     `gorm:"size:16;not null;index:idx_touch_due,priority:2" json:"kind"`                   // bill/domains
	Payload   string     `gorm:"type:text;not null" json:"payload"`                                             // 渲染内容 JSON {title,body}
	Status    string     `gorm:"size:16;not null;default:pending;index:idx_touch_due,priority:1" json:"status"` // pending/sent/failed
	EventID   int64      `gorm:"default:0" json:"event_id"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	Error     string     `gorm:"size:255" json:"error,omitempty"`
	CreatedAt time.Time  `gorm:"not null" json:"created_at"`
}

// EntryDomain 是面板入口域名与备用地址（触达批 TOUCH-3）：域名例行邮件与
// 断联容灾的共同数据源（订阅备用信息、推新入口都取这份清单）。
type EntryDomain struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Domain    string    `gorm:"size:255;not null" json:"domain"`
	Role      string    `gorm:"size:16;not null" json:"role"`    // primary/backup
	Region    string    `gorm:"size:64" json:"region,omitempty"` // 适用区域，空=全区域
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`
}

// SaveStat 是流量节省按日汇总（SAVE-7）：从 node_traffic_logs 聚合，
// 直连/拦截字节来自 agent 出站计数，缓存命中预留（缓存层字节指标接
// SAVE-3 metrics 后汇入），折算费用按节点流量单价在 API 侧计算。
type SaveStat struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	NodeID        uint      `gorm:"uniqueIndex:idx_save_stats;not null" json:"node_id"`
	Day           string    `gorm:"size:10;not null;uniqueIndex:idx_save_stats" json:"day"` // YYYY-MM-DD（UTC）
	DirectBytes   int64     `gorm:"default:0" json:"direct_bytes"`
	CacheHitBytes int64     `gorm:"default:0" json:"cache_hit_bytes"`
	BlockedBytes  int64     `gorm:"default:0" json:"blocked_bytes"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PaymentOrder 是订单（三账之一：谁该收多少）。
type PaymentOrder struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	OrderNo     string `gorm:"size:32;uniqueIndex" json:"order_no"`
	UserID      uint   `gorm:"index;not null" json:"user_id"`
	Provider    string `gorm:"size:16" json:"provider"` // card / epusdt / wechat / alipay
	AmountCents int64  `gorm:"default:0" json:"amount_cents"`
	Product     string `gorm:"size:128" json:"product"`
	Status      string `gorm:"size:16;default:pending;index" json:"status"` // pending/paid/failed/expired/refunded
	// 代理归属（DS-1）：兑换/下单时从卡批次或商品带来，0=自营。
	DistributorID uint `gorm:"default:0;index" json:"distributor_id"`
	// 到账自动发放口径（在线支付用，卡密走批次自带）：add_quota/extend_days，空=不自动发放。
	GrantType  string     `gorm:"size:16" json:"grant_type,omitempty"`
	GrantValue int64      `gorm:"default:0" json:"grant_value,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	PaidAt     *time.Time `json:"paid_at"`
	// 退款留痕（OD-2）：面板只做状态流转与记录，钱款退回经渠道后台操作。
	RefundAt   *time.Time `json:"refund_at,omitempty"`
	RefundNote string     `gorm:"size:255" json:"refund_note,omitempty"` // 渠道退款单号/原因
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

// Distributor 是代理账号（DS 分销，独立登录体系，非 users：无配额/订阅语义）。
// 停用即禁登录，账目保留。佣金比例=面板让利部分归代理。
type Distributor struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Username        string    `gorm:"size:64;uniqueIndex;not null" json:"username"`
	PasswordHash    string    `gorm:"size:128;not null" json:"-"`
	DiscountPercent int       `gorm:"default:0" json:"discount_percent"` // 佣金比例 0-100
	Note            string    `gorm:"size:255" json:"note"`
	Enabled         bool      `gorm:"default:true" json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// DistributorLedger 是代理账目流水：sale（售卡留痕不进余额）/commission（入余额）/
// payout（结算扣减）/adjust（人工调）。未结算余额 = Σcommission + Σadjust − Σpayout。
type DistributorLedger struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	DistributorID uint      `gorm:"index;not null" json:"distributor_id"`
	OrderNo       string    `gorm:"size:32;index" json:"order_no"` // sale/commission 与三账订单串联
	Kind          string    `gorm:"size:16;not null" json:"kind"`  // sale/commission/payout/adjust
	AmountCents   int64     `gorm:"not null" json:"amount_cents"`  // 接口层以正负区分入账/扣减
	Note          string    `gorm:"size:255" json:"note"`
	CreatedAt     time.Time `json:"created_at"`
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
	Plan              string    `gorm:"size:64" json:"plan"`                 // 机型 slug
	Region            string    `gorm:"size:64" json:"region"`               // 区域
	Image             string    `gorm:"size:64" json:"image"`                // 系统镜像（provider 各自口径）
	BwMbps            int       `json:"bw_mbps"`                             // 带宽（Mbps）
	BillingType       string    `gorm:"size:16" json:"billing_type"`         // 包月 / 按流量
	MonthlyCostCents  int64     `json:"monthly_cost_cents"`                  // 月固定成本（分）
	TrafficPriceCents int64     `json:"traffic_price_cents"`                 // 流量单价（分/GB）
	Direction         string    `gorm:"size:8;default:out" json:"direction"` // out/in/both
	LineType          string    `gorm:"size:32" json:"line_type"`            // 163/cn2_gia/cu_vip/cmi/iplc
	Role              string    `gorm:"size:16;default:entry" json:"role"`   // entry/landing/both/relay
	Transport         string    `gorm:"size:16" json:"transport"`            // tls/ws-tls/quic/ssh
	Config            string    `gorm:"type:text" json:"config"`             // 协议配置模板 JSON（OS-4：开服复制到节点行，供自动下发）
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ProvisionJob 是一次供给执行留痕（OS-2）：plan/apply 受控执行的结果与
// 日志尾部。单实例不并发 apply；日志不含机密（密钥走 TF_VAR 环境变量）。
type ProvisionJob struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	TemplateID   uint       `gorm:"index;not null" json:"template_id"`
	TemplateName string     `gorm:"size:64" json:"template_name"`
	Action       string     `gorm:"size:8;not null" json:"action"` // plan / apply
	Status       string     `gorm:"size:8;not null" json:"status"` // running / ok / failed
	Log          string     `gorm:"type:text" json:"log"`
	CreatedAt    time.Time  `json:"created_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

// Recovery 是封禁恢复流水线状态行（BR-1）：每节点至多一条进行中，
// L1→L2→L3 分级推进，每级超时未恢复进下一级，探测恢复（摘挂复位）
// 即完成。动作级留痕与失败升级人工由后续 recovery_actions 承接（BR-5）。
type Recovery struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	NodeID         uint       `gorm:"uniqueIndex;not null" json:"node_id"`
	NodeName       string     `gorm:"size:64" json:"node_name"`
	Level          int        `json:"level"` // 当前推进级别 1-3
	State          string     `gorm:"size:16" json:"state"`
	Action         string     `gorm:"size:32" json:"action"`       // 当前级别动作名（空=未开跑）
	ActionState    string     `gorm:"size:16" json:"action_state"` // running/ok/failed/skipped/空
	LastErr        string     `gorm:"size:512" json:"last_error,omitempty"`
	LevelStartedAt time.Time  `json:"level_started_at"`
	StartedAt      time.Time  `json:"started_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

// RecoveryAction 是恢复流水线每级动作的留痕（BR-5）：一次推进一条，
// 回放用——从判封到终态每级谁在跑、成没成、为何失败全可查。
type RecoveryAction struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	RecoveryID uint       `gorm:"index;not null" json:"recovery_id"`
	NodeID     uint       `json:"node_id"`
	NodeName   string     `gorm:"size:64" json:"node_name"`
	Level      int        `json:"level"`
	Action     string     `gorm:"size:32" json:"action"`            // 动作名（未注册级为空）
	State      string     `gorm:"size:16" json:"state"`             // running/ok/failed/skipped/timeout
	Detail     string     `gorm:"size:512" json:"detail,omitempty"` // 失败/跳过原因
	StartedAt  time.Time  `json:"started_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// DNSProvider 是 DNS 商凭证（BR-2 插件位；BR-4 ACME DNS-01 复用同一通道）。
// 凭证机密走 R24 加密面口径，接口层只回 has_api_key 不回显明文。
type DNSProvider struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:64;not null" json:"name"`
	Type      string    `gorm:"size:32;not null" json:"type"` // 插件位类型：cloudflare/…
	APIKey    string    `gorm:"size:512;not null" json:"-"`   // 密文
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DNSFront 是域名前置记录（BR-2）：常态指向 PrimaryIP，判封 L1 切到
// 备用 IP 轮换，探测恢复回切——域名不换、IP 随换。BackupIPs 是 JSON
// 字符串数组，SwitchIndex 为轮换游标。
type DNSFront struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:64" json:"name"`                   // 说明（如 主入口域名）
	Domain      string    `gorm:"size:191;not null;index" json:"domain"` // FQDN
	ProviderID  uint      `gorm:"index;not null" json:"provider_id"`
	PrimaryIP   string    `gorm:"size:45;not null" json:"primary_ip"`              // 常态指向（回切目标）
	BackupIPs   string    `gorm:"size:1024;not null;default:[]" json:"backup_ips"` // JSON 数组
	Switched    bool      `gorm:"default:false" json:"switched"`                   // 已切离常态（恢复时回切）
	CurrentIP   string    `gorm:"size:45" json:"current_ip"`
	SwitchIndex int       `gorm:"default:0" json:"switch_index"` // 备用 IP 轮换游标
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GeoDNSRecord 是智能 DNS 分地域的已同步记录面（E-27 §C.8.4）：对账
// 的 diff 基准——Sync 期望集与现存行比对，新增/变更 Upsert、消失 Delete
//（区域全挂撤记录）。不回写 entry_domains 人工清单（分地域是增益不是依赖）。
type GeoDNSRecord struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	FrontID   uint      `gorm:"index;not null" json:"front_id"` // 归属 DNSFront
	Slug      string    `gorm:"size:64;not null" json:"slug"`   // 区域归一词
	Name      string    `gorm:"size:253;not null;index" json:"name"` // 完整子域 {slug}.{前置域名}
	Value     string    `gorm:"size:255;not null" json:"value"`      // 代表入口地址
	UpdatedAt time.Time `json:"updated_at"`
}

// CertTask 是一张证书的编排任务（BR-4）：面板管编排与到期，签发执行
// 复用外部工具（acme.sh）——DNS-01 复用 BR-2 的 DNS 商凭证通道，
// 产物留在工具工作目录，节点分发由后续批次接。
type CertTask struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	Name          string     `gorm:"size:64" json:"name"`                           // 说明
	Domain        string     `gorm:"size:191;not null;index" json:"domain"`         // 主域名（FQDN）
	Sans          string     `gorm:"size:512" json:"sans"`                          // 附加域名（逗号分隔，可空）
	Method        string     `gorm:"size:16;not null;default:dns-01" json:"method"` // dns-01/http-01
	DNSProviderID uint       `json:"provider_id"`                                   // dns-01 用的 DNS 商凭证（BR-2 通道）
	CA            string     `gorm:"size:32;default:letsencrypt" json:"ca"`
	State         string     `gorm:"size:16;default:pending" json:"state"` // pending/issuing/ok/failed
	NotAfter      *time.Time `json:"not_after"`                            // 证书到期（签发成功后回填）
	LastError     string     `gorm:"size:512" json:"last_error,omitempty"`
	LastAttempt   *time.Time `json:"last_attempt"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// Notification 是一条站内信（NT-1）：面向用户的四类通知（公告/到期/流量
// 预警/系统），逐用户落行（小用户量扇出成本可忽略），已读态挂在行上。
type Notification struct {
	ID        int64      `gorm:"primaryKey" json:"id"`
	UserID    int64      `gorm:"index;not null" json:"user_id"`      // 收件人
	Type      string     `gorm:"size:16;index;not null" json:"type"` // announcement/expiry/traffic/system
	Title     string     `gorm:"size:128;not null" json:"title"`
	Body      string     `gorm:"size:512" json:"body,omitempty"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// 通知类型（NT-1/NT-2）：announcement 公告扇出；expiry/traffic 到期与流量
// 阈值的定时扫描触发；system 事件触发（发放到账等）。
const (
	NotifAnnouncement = "announcement"
	NotifExpiry       = "expiry"
	NotifTraffic      = "traffic"
	NotifSystem       = "system"
)

// QuotaAction 是一条配额联动留痕（SAVE-6）：用户流量/费用超阈值后订阅自动
// 降档（只出低成本档入口），released_at 空=降档生效中；阈值字段为触发时刻
// 快照，降档订阅按快照档线过滤，设置后续调整不影响已生效行。
type QuotaAction struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	UserID        uint       `gorm:"index;not null" json:"user_id"`
	Trigger       string     `gorm:"size:16;not null" json:"trigger"`  // traffic/cost
	UsedBytes     int64      `gorm:"default:0" json:"used_bytes"`      // 触发时窗口用量快照
	QuotaBytes    int64      `gorm:"default:0" json:"quota_bytes"`     // 触发时个人配额快照（流量档）
	CostCents     int64      `gorm:"default:0" json:"cost_cents"`      // 触发时折算费用快照（分，费用档）
	MaxPriceCents int64      `gorm:"default:0" json:"max_price_cents"` // 降档成本档线快照（分/GB）
	Reason        string     `gorm:"size:255" json:"reason"`
	CreatedAt     time.Time  `gorm:"not null" json:"created_at"`
	ReleasedAt    *time.Time `json:"released_at"`
	ReleaseReason string     `gorm:"size:64" json:"release_reason"`
}

// Event 是一条事件 outbox 行（HERALD-1，《告警通道设计》§4/§5）：事件生产侧
// 落库即返回，投递由 herald.Loop 异步重试，通道故障不静默丢。
type Event struct {
	ID            int64      `gorm:"primaryKey" json:"id"`
	Kind          string     `gorm:"size:32;index;not null" json:"kind"` // region_fault/node_blocked/cert_expiring/...（§2 清单）
	Severity      string     `gorm:"size:16;not null" json:"severity"`   // critical/warning/info
	Title         string     `gorm:"size:255;not null" json:"title"`
	Body          string     `gorm:"size:1024" json:"body,omitempty"`
	Target        string     `gorm:"size:32;not null" json:"target"` // admin / user:<id>
	DedupKey      string     `gorm:"size:128" json:"dedup_key,omitempty"`
	Meta          string     `gorm:"type:text" json:"meta,omitempty"`                      // JSON
	Status        string     `gorm:"size:16;index;not null;default:pending" json:"status"` // pending/sent/failed
	Attempts      int        `gorm:"default:0" json:"attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"` // 空=立即可投
	OccurredAt    time.Time  `gorm:"not null" json:"occurred_at"`
	CreatedAt     time.Time  `gorm:"not null" json:"created_at"`
}

// EventDelivery 是一条投递留痕：ferry→Herald 的每次投递尝试结果，以及
// Herald 异步回投的通道分发回执（HERALD-2 起回填 channel 维度）。
type EventDelivery struct {
	ID      int64     `gorm:"primaryKey" json:"id"`
	EventID int64     `gorm:"index;not null" json:"event_id"`
	Channel string    `gorm:"size:16;not null" json:"channel"` // herald（ferry→Herald 投递腿）/tg/email/wechat/webhook
	Status  string    `gorm:"size:16;not null" json:"status"`  // sent/failed
	Detail  string    `gorm:"size:512" json:"detail,omitempty"`
	At      time.Time `gorm:"not null" json:"at"`
}

// Backup 是一条备份留痕（面板可用性 §6，P1）：kind=manual 为手动下载
// 端点落档、scheduled 为周期备份 Loop 落档；path 指向备份目录内的档文件
// （配置了主密钥时为加密档 .enc）。uploaded 是外发位成功标记——周期备份
// S3 外发（FERRY_BACKUP_S3_*，默认关闭）上传成功才置 1，manual 落档恒 0。
type Backup struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Kind      string    `gorm:"size:16;index;not null" json:"kind"` // manual / scheduled
	Path      string    `gorm:"size:255" json:"path"`
	SizeBytes int64     `json:"size_bytes"`
	Uploaded  bool      `gorm:"default:false" json:"uploaded"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

// RecoveryCode 是两步验证的一次性恢复码（安全设计 §1，P1）：只存 sha256
// 哈希不存明文（明文仅绑定完成时一次性下发展示），用一个销一个——used_at
// 非空即已消费，比对与销毁同事务防重放。
type RecoveryCode struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	UserID    uint       `gorm:"index;not null" json:"user_id"`
	CodeHash  string     `gorm:"size:64;not null" json:"-"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// LoginLog 是管理员登录审计（安全设计 §1，P1，对齐设计 DDL）：时间/IP/
// UA/结果，dash 分页可查；连续失败与新网段登录经 Herald login_alert 告警。
type LoginLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Username  string    `gorm:"size:64;index" json:"username"`
	IP        string    `gorm:"size:64" json:"ip"`
	UA        string    `gorm:"size:255" json:"ua"`
	OK        bool      `json:"ok"`
	CreatedAt time.Time `gorm:"index" json:"created_at"` // 设计 DDL idx_login_logs_time
}

// PriceWatch 是价格关注条件（E-32，套餐与成本设计 §4.2 DDL 蓝本）：盯
// 「商家+区域+配置档」一个牌价键，降价或现价到位（target_price>0）经
// Herald price_alert 提示；条件与参考价同词表（costref 价格表键）。
type PriceWatch struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Provider    string    `gorm:"size:128;not null;uniqueIndex:idx_price_watch" json:"provider"`
	Region      string    `gorm:"size:128;not null;default:'';uniqueIndex:idx_price_watch" json:"region"`
	Spec        string    `gorm:"size:128;not null;uniqueIndex:idx_price_watch" json:"spec"`
	TargetPrice int64     `gorm:"default:0" json:"target_price"` // 目标价位（分/月），0=只盯降价
	Enabled     bool      `gorm:"default:true" json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PriceSnapshot 是一次关注键的牌价快照（扫描周期从参考价表抓取，比对
// 降价与到位沿用；设计 DDL REFERENCES 级联改为应用层：删关注同删快照）。
type PriceSnapshot struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	WatchID      uint      `gorm:"index;not null" json:"watch_id"`
	MonthlyCents int64     `gorm:"not null" json:"monthly_cents"`
	Source       string    `gorm:"size:64;not null" json:"source"`
	URL          string    `gorm:"size:255" json:"url,omitempty"`
	CapturedAt   time.Time `gorm:"not null;index" json:"captured_at"`
}
