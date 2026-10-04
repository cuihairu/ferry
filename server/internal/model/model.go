// Package model 定义跨层共享的数据结构与请求/响应载荷。
package model

import "time"

// 支持的节点协议，范围口径见 todo P2-7：只做四种主流协议。
const (
	ProtoVless       = "vless"
	ProtoVmess       = "vmess"
	ProtoTrojan      = "trojan"
	ProtoShadowsocks = "shadowsocks"
)

// ValidProtocol 判断协议是否在支持范围内。
func ValidProtocol(p string) bool {
	switch p {
	case ProtoVless, ProtoVmess, ProtoTrojan, ProtoShadowsocks:
		return true
	}
	return false
}

// Node 是一台代理节点的描述。config 为该协议的配置模板 JSON 文本。
type Node struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Address   string     `json:"address"`
	Port      int        `json:"port"`
	Protocol  string     `json:"protocol"`
	Config    string     `json:"config"`
	Enabled   bool       `json:"enabled"`
	Token     string     `json:"token"` // agent 出站连接令牌，管理面可见
	LastSeen  *time.Time `json:"last_seen,omitempty"`
	Status    string     `json:"status"` // online/offline/unknown
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// NodeInput 是创建/更新节点的请求载荷。
type NodeInput struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Config   string `json:"config"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

// User 是一个订阅用户。
type User struct {
	ID         int64      `json:"id"`
	Username   string     `json:"username"`
	SubToken   string     `json:"sub_token"` // 订阅链接令牌
	QuotaBytes int64      `json:"quota_bytes"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// UserInput 是创建/更新用户的请求载荷。
type UserInput struct {
	Username   string     `json:"username"`
	QuotaBytes *int64     `json:"quota_bytes,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Enabled    *bool      `json:"enabled,omitempty"`
}
