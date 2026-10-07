// Package herald 事件 outbox（《告警通道设计》HERALD-1）：ferry 不自建投递
// 通道——事件（管理告警 + 用户触达）统一落本地 outbox 后异步投递 Herald，
// 通道分发（TG/微信/邮件）由 Herald 管。面板离线/Herald 不可达时事件不丢
// （pending 重试、超限标红 failed 死信），恢复后补投。
package herald

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 事件状态与严重度取值。
const (
	StatusPending = "pending" // 待投/重试中
	StatusSent    = "sent"    // 已投出（Herald 接收成功）
	StatusFailed  = "failed"  // 重试超限死信（dash 标红，通道故障不静默丢）

	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
)

// 投递腿 channel 名：ferry→Herald 的 POST 结果按此落 event_deliveries；
// Herald 异步回投的通道分发回执（HERALD-2）用具体通道名。
const ChannelHerald = "herald"

// 事件 kind 与 target 取值（《告警通道设计》§2 清单，HERALD-3 起按类接入）。
const (
	KindRegionFault    = "region_fault"    // 区域聚合整体故障
	KindISPFault       = "isp_fault"       // 运营商聚合整体故障
	KindNodeBlocked    = "node_blocked"    // 节点判封进恢复流水线
	KindNodeDown       = "node_down"       // 单点自动摘挂
	KindCostExceeded   = "cost_exceeded"   // 高成本告警激活
	KindProcCrashed    = "proc_crashed"    // 进程崩溃拉起失败
	KindRecoveryFailed = "recovery_failed" // 恢复流水线全级耗尽（升级人工）
	KindCertExpiring   = "cert_expiring"   // 证书临近到期

	// 用户触达（HERALD-4，§2.2；站内信照发，本通道供站外投递）。
	KindExpire = "expire" // 账号到期提醒
	KindQuota  = "quota"  // 流量预警
	KindOrder  = "order"  // 发放/订单结果
	KindNotice = "notice" // 公告

	// 例行触达（TOUCH-4，§5）：账单类默认必收，域名例行可退订。
	KindBill    = "bill"    // 月账单（用量/状态/到期/续费入口/已省下亮点）
	KindDomains = "domains" // 入口域名例行清单（保新鲜，可退订）
	KindContact = "contact" // 联系方式连续投递失败升级（换通道再试，TOUCH-5）

	TargetAdmin = "admin"
)

// TargetUser 用户维度的 target 取值（user:<id>）。
func TargetUser(id int64) string {
	return fmt.Sprintf("user:%d", id)
}

// ValidSeverity 严重度取值是否合法。
func ValidSeverity(s string) bool {
	switch s {
	case SeverityCritical, SeverityWarning, SeverityInfo:
		return true
	}
	return false
}

// EmitInput 是事件生产侧的载荷（HERALD-3 起管理告警九类、触达批起用户事件
// 都走这里）。Meta 为任意可 JSON 序列化对象，落库为 JSON 文本。
type EmitInput struct {
	Kind       string
	Severity   string
	Title      string
	Body       string
	Target     string // admin / user:<id>
	DedupKey   string // 去重键兜底（窗口内同键合并由 Herald 做，ferry 侧只透传）
	Meta       any
	OccurredAt time.Time
}

// Emit 落一条 pending 事件并立即返回（outbox 语义：生产侧不碰网络）。
func Emit(db *gorm.DB, in EmitInput) (*storage.Event, error) {
	if !ValidSeverity(in.Severity) {
		in.Severity = SeverityInfo
	}
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now()
	}
	now := time.Now()
	meta := ""
	if in.Meta != nil {
		if raw, err := json.Marshal(in.Meta); err == nil {
			meta = string(raw)
		}
	}
	row := storage.Event{
		Kind: in.Kind, Severity: in.Severity,
		Title: in.Title, Body: in.Body,
		Target: in.Target, DedupKey: in.DedupKey, Meta: meta,
		Status: StatusPending, OccurredAt: in.OccurredAt, CreatedAt: now,
	}
	if err := db.Create(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// Count 取各状态事件计数（dash 红标口径：failed>0 即通道故障未消化）。
func Count(db *gorm.DB) (pending, sent, failed int64, err error) {
	type row struct {
		Status string
		N      int64
	}
	var rows []row
	if err = db.Model(&storage.Event{}).Select("status, COUNT(*) AS n").Group("status").Scan(&rows).Error; err != nil {
		return
	}
	for _, r := range rows {
		switch r.Status {
		case StatusPending:
			pending = r.N
		case StatusSent:
			sent = r.N
		case StatusFailed:
			failed = r.N
		}
	}
	return
}
