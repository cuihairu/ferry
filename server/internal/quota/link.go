// Package quota 的配额联动（SAVE-6，《流量节省设计》§5）：用户流量/费用
// 超阈值 → 订阅自动降档——该用户订阅只出低成本档入口（包月零边际成本或
// 单价不超档线的节点），新连接自然落到低成本落地，存量连接不动。
// 降速动作不落：节点共享凭据无按用户身份，带宽整形无从挂靠（P2-3 口径），
// 联动动作收敛为「订阅降档」一种，触发与释放都留痕（quota_actions）。
package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// settingKey 联动配置的持久化键（panel KV）。
const settingKey = "quota_link"

// 触发器取值（QuotaAction.Trigger）。
const (
	LinkTriggerTraffic = "traffic" // 流量档：窗口用量达个人配额百分比
	LinkTriggerCost    = "cost"    // 费用档：窗口折算费用达绝对阈值
)

// 缺省档：流量档 90%（预警 80% 之后留一段缓冲再降档）。
const DefaultTrafficPercent = 90

// LinkSetting 联动配置（dash 可配，默认关）。两档触发都关时不可启用。
type LinkSetting struct {
	Enabled bool `json:"enabled"`
	// TrafficPercent 流量档阈值（1-100）：窗口用量达个人配额的 N% 触发；
	// 0=不按流量档触发。仅对配额 >0 的用户生效。
	TrafficPercent int `json:"traffic_percent"`
	// CostCents 费用档阈值（分）：窗口折算费用（按流量节点逐条记账乘单价）
	// 达 N 分触发；0=不按费用档触发。配额 0（不限量）用户的唯一触发档。
	CostCents int64 `json:"cost_cents"`
	// MaxPriceCents 降档成本档线（分/GB）：包月（零边际成本）或单价不超线
	// 的按流量节点视为低成本档。0=只有包月节点算低成本档。
	MaxPriceCents int64 `json:"max_price_cents"`
}

// DefaultLinkSetting 缺省配置：关。启用时流量档给 90% 作起点。
func DefaultLinkSetting() LinkSetting {
	return LinkSetting{Enabled: false, TrafficPercent: DefaultTrafficPercent}
}

// LoadLinkSetting 读取联动配置；未设置回缺省，脏数据按缺省走不阻断。
func LoadLinkSetting(db *gorm.DB) (LinkSetting, error) {
	setting := DefaultLinkSetting()
	raw, ok, err := storage.GetSetting(db, settingKey)
	if err != nil || !ok {
		return setting, err
	}
	if err := json.Unmarshal([]byte(raw), &setting); err != nil {
		return DefaultLinkSetting(), nil
	}
	setting.normalize()
	return setting, nil
}

// normalize 越界字段归位：阈值负数或超界按缺省/关闭处理。
func (s *LinkSetting) normalize() {
	if s.TrafficPercent < 0 || s.TrafficPercent > 100 {
		s.TrafficPercent = DefaultTrafficPercent
	}
	if s.CostCents < 0 {
		s.CostCents = 0
	}
	if s.MaxPriceCents < 0 {
		s.MaxPriceCents = 0
	}
}

// SaveLinkSetting 校验并持久化联动配置：启用时至少要有一档触发阈值，
// 否则联动形同虚设还占着「已启用」的语义，直接拒绝。
func SaveLinkSetting(db *gorm.DB, setting LinkSetting) error {
	setting.normalize()
	if setting.Enabled && setting.TrafficPercent == 0 && setting.CostCents == 0 {
		return errors.New("enabled 联动至少要配置一个触发阈值（traffic_percent 或 cost_cents）")
	}
	raw, err := json.Marshal(setting)
	if err != nil {
		return err
	}
	return storage.SetSetting(db, settingKey, string(raw))
}

// Trigger 判定用户是否触发联动，返回命中的触发器（都未命中返回空串，
// 流量档优先）。used 为窗口用量，costCents 为窗口折算费用。
func (s LinkSetting) Trigger(used, quotaBytes, costCents int64) string {
	if !s.Enabled {
		return ""
	}
	if s.TrafficPercent > 0 && quotaBytes > 0 && used*100 >= quotaBytes*int64(s.TrafficPercent) {
		return LinkTriggerTraffic
	}
	if s.CostCents > 0 && costCents >= s.CostCents {
		return LinkTriggerCost
	}
	return ""
}

// LowCost 节点是否属低成本档（降档订阅可见）：包月/固定带宽零边际成本，
// 按流量节点单价不超档线。口径对齐 alloc.Candidate.CostPerGB——只有
// 「按流量」计费有边际成本。
func (s LinkSetting) LowCost(n storage.Node) bool {
	if n.BillingType != "按流量" {
		return true
	}
	return n.TrafficPriceCents <= s.MaxPriceCents
}

// ActiveAction 取用户生效中的联动降档行（订阅出口按其快照档线过滤）；
// 无生效行返回 nil。
func ActiveAction(db *gorm.DB, userID uint) (*storage.QuotaAction, error) {
	var row storage.QuotaAction
	err := db.Where("user_id = ? AND released_at IS NULL", userID).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// WindowCostCents 折算用户窗口流量费用（分）：逐条记账乘所在按流量节点
// 单价（分/GB）后按 GB 整除求和；包月/固定带宽零边际成本不计（对齐
// alloc 成本口径）。单价乘在字节上、最后整除 2^30，小流量零头向下取整。
func WindowCostCents(db *gorm.DB, userID uint, cycle string, now time.Time) (int64, error) {
	q := db.Model(&storage.TrafficLog{}).
		Select("COALESCE(SUM((traffic_logs.rx_bytes + traffic_logs.tx_bytes) * nodes.traffic_price_cents), 0)").
		Joins("LEFT JOIN nodes ON nodes.id = traffic_logs.node_id").
		Where("traffic_logs.user_id = ? AND nodes.billing_type = ?", userID, "按流量")
	if since := WindowStart(cycle, now); !since.IsZero() {
		q = q.Where("traffic_logs.recorded_at >= ?", since)
	}
	var raw int64
	if err := q.Scan(&raw).Error; err != nil {
		return 0, err
	}
	return raw / (1 << 30), nil
}

// Sweep 执行一轮联动判定：触发者建降档留痕并发站内信（已有生效中行不重复
// 建不重复发）；不再触发（用量回落/窗口滚动/阈值放宽）或联动被关的释放
// 留痕。返回本轮新建的降档行数。
func Sweep(db *gorm.DB, now time.Time) (int, error) {
	setting, err := LoadLinkSetting(db)
	if err != nil {
		return 0, err
	}
	if !setting.Enabled {
		return 0, releaseAll(db, now, "联动关闭")
	}
	var users []storage.User
	if err := db.Where("enabled = ?", true).Find(&users).Error; err != nil {
		return 0, err
	}
	created := 0
	for _, u := range users {
		active, err := ActiveAction(db, u.ID)
		if err != nil {
			return created, err
		}
		used, err := UsedBytes(db, u.ID, u.ResetCycle, now)
		if err != nil {
			return created, err
		}
		cost, err := WindowCostCents(db, u.ID, u.ResetCycle, now)
		if err != nil {
			return created, err
		}
		trigger := setting.Trigger(used, u.QuotaBytes, cost)
		switch {
		case trigger != "" && active == nil:
			if err := apply(db, now, u, setting, trigger, used, cost); err != nil {
				return created, err
			}
			created++
		case trigger == "" && active != nil:
			if err := db.Model(&storage.QuotaAction{}).Where("id = ?", active.ID).
				Updates(map[string]any{"released_at": now, "release_reason": "阈值回落"}).Error; err != nil {
				return created, err
			}
		}
	}
	return created, nil
}

// apply 落一条降档留痕并发站内信。reason 带触发口径（百分比/金额），
// 对齐 landing_assignments.reason 的留痕习惯。
func apply(db *gorm.DB, now time.Time, u storage.User, setting LinkSetting, trigger string, used, cost int64) error {
	reason := fmt.Sprintf("费用达阈值 ¥%.2f", float64(cost)/100)
	if trigger == LinkTriggerTraffic {
		reason = fmt.Sprintf("流量达配额 %d%%（%s/%s）", setting.TrafficPercent, humanMB(used), humanMB(u.QuotaBytes))
	}
	row := storage.QuotaAction{
		UserID: u.ID, Trigger: trigger,
		UsedBytes: used, QuotaBytes: u.QuotaBytes, CostCents: cost,
		MaxPriceCents: setting.MaxPriceCents,
		Reason:        reason, CreatedAt: now,
	}
	if err := db.Create(&row).Error; err != nil {
		return err
	}
	// 站内信（NT 体系 system 类，单用户直落）：告知降档与恢复口径。
	return db.Create(&storage.Notification{
		UserID: int64(u.ID), Type: storage.NotifSystem,
		Title:     "已达用量阈值，订阅已切换低成本线路",
		Body:      reason + "；用量回落后订阅将自动恢复完整节点列表。",
		CreatedAt: now,
	}).Error
}

// humanMB 字节的人类可读量级（MB/GB 向上取整），只用于留痕文案。
func humanMB(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%dGB", (b+(1<<30)-1)>>30)
	case b >= 1<<20:
		return fmt.Sprintf("%dMB", (b+(1<<20)-1)>>20)
	default:
		return fmt.Sprintf("%dB", b)
	}
}

// releaseAll 释放全部生效中的降档行（联动被关时兜底清场）。
func releaseAll(db *gorm.DB, now time.Time, reason string) error {
	return db.Model(&storage.QuotaAction{}).Where("released_at IS NULL").
		Updates(map[string]any{"released_at": now, "release_reason": reason}).Error
}

// LinkLoop 周期执行联动判定直到 ctx 取消（对齐 alloc.Loop：启动即跑一轮，
// 联动状态在订阅出口即时生效，loop 只负责状态迁移与通知）。
func LinkLoop(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if n, err := Sweep(db, time.Now()); err != nil {
			logger.Printf("quota link sweep: %v", err)
		} else if n > 0 {
			logger.Printf("quota link: %d user(s) downgraded", n)
		}
		t.Reset(interval)
	}
}
