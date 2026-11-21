package storage

import "time"

// Subscription 是用户自建的独立订阅（ferry-sub-filters，2026-11-21 用户拍板）：
// 同一用户可建多个，各自独立 token、过滤维度（region/labels/protocol）与
// per-sub 配置（remark_template/sort/max_nodes/include_rtt）。
// Filter/Config 是 JSON 文本（严格校验在写入侧，见 sub.ParseFilter/ParseConfig），
// 下发侧在内存中解析应用；Token 由服务端 crypto/rand 生成（同 RandomToken 口径）。
type Subscription struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index" json:"user_id"`
	Token     string    `gorm:"size:64;uniqueIndex" json:"token"`
	Name      string    `gorm:"size:64" json:"name"`
	Filter    string    `gorm:"type:text" json:"filter"`
	Config    string    `gorm:"type:text" json:"config"`
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
