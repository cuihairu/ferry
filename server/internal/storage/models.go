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
	ID        uint       `gorm:"primaryKey" json:"id"`
	Name      string     `gorm:"size:64" json:"name"`
	Address   string     `gorm:"size:255" json:"address"`
	Port      int        `gorm:"not null" json:"port"`
	Protocol  string     `gorm:"size:16" json:"protocol"` // vless/vmess/trojan/shadowsocks
	Config    string     `gorm:"type:text" json:"config"` // 协议配置模板 JSON
	Enabled   bool       `gorm:"default:true" json:"enabled"`
	Token     string     `gorm:"size:64;uniqueIndex" json:"token"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
	Status    string     `gorm:"size:16;default:unknown" json:"status"` // online/offline/unknown
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
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
