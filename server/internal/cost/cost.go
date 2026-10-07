// Package cost 成本看板与高成本告警（E-23）：节点流量花费（仅「按流量」
// 计费有边际成本）、区域/运营商汇总、月度预估（自然月至今按日折算），
// 流量花费超阈值节点单发告警提示切流；月固定成本是沉没成本只进汇总，
// 不参与分配判定（与 alloc 口径一致）。
package cost

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 口径常量。
const (
	// BytesPerGB 十进制 GB（与流量单价「分/GB」同口径）。
	BytesPerGB = 1_000_000_000
	// settingThreshold 是阈值持久化键（分/月）。
	settingThreshold = "cost_alert_threshold_cents"
	// DefaultThresholdCents 缺省阈值：¥50/月。
	DefaultThresholdCents = 5000
	// AlertKindHighCost 是高成本告警类别（复用 alerts 表）。
	AlertKindHighCost = "high_cost"
)

// 告警状态取值（与 handler 侧 alerts 表口径一致）。
const (
	alertActive   = "active"
	alertResolved = "resolved"
)

// NodeCost 是一个节点的月度成本视图。
type NodeCost struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Region       string `json:"region"`
	ISP          string `json:"isp"`
	BillingType  string `json:"billing_type"`
	MonthRxBytes int64  `json:"month_rx_bytes"`
	MonthTxBytes int64  `json:"month_tx_bytes"`
	// TrafficCostCents 月至今流量花费（分，仅按流量计费节点非零）。
	TrafficCostCents int64 `json:"traffic_cost_cents"`
	// FixedCostCents 月固定成本（分，包月/固定带宽口径）。
	FixedCostCents int64 `json:"fixed_cost_cents"`
	// ProjectedCents 月度预估（分）：固定全月 + 流量按日折算。
	ProjectedCents int64 `json:"projected_cents"`
}

// GroupCost 是区域/运营商维度的成本汇总。
type GroupCost struct {
	Key            string `json:"key"`
	Nodes          int    `json:"nodes"`
	TrafficCents   int64  `json:"traffic_cents"`
	FixedCents     int64  `json:"fixed_cents"`
	ProjectedCents int64  `json:"projected_cents"`
}

// Report 是成本看板数据。
type Report struct {
	Nodes          []NodeCost  `json:"nodes"`
	Regions        []GroupCost `json:"regions"`
	Isps           []GroupCost `json:"isps"`
	Summary        GroupCost   `json:"summary"`
	ThresholdCents int64       `json:"threshold_cents"`
}

// GetThreshold 读取告警阈值（分/月）；未设置或脏数据回缺省。
func GetThreshold(db *gorm.DB) (int64, error) {
	raw, ok, err := storage.GetSetting(db, settingThreshold)
	if err != nil || !ok {
		return DefaultThresholdCents, err
	}
	var cents int64
	if _, err := fmt.Sscanf(raw, "%d", &cents); err != nil || cents < 0 {
		return DefaultThresholdCents, nil
	}
	return cents, nil
}

// SaveThreshold 校验并持久化告警阈值（分/月）。
func SaveThreshold(db *gorm.DB, cents int64) error {
	if cents < 0 {
		return errors.New("threshold must be >= 0")
	}
	return storage.SetSetting(db, settingThreshold, fmt.Sprintf("%d", cents))
}

// Build 汇总自然月至今各节点流量与成本，按区域/运营商聚合。
func Build(db *gorm.DB, now time.Time) (*Report, error) {
	threshold, err := GetThreshold(db)
	if err != nil {
		return nil, err
	}
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	var nodes []storage.Node
	if err := db.Find(&nodes).Error; err != nil {
		return nil, err
	}
	traffic := map[uint][2]int64{} // node → [rx, tx]
	var rows []struct {
		NodeID uint
		Rx     int64
		Tx     int64
	}
	if err := db.Model(&storage.NodeTrafficLog{}).
		Select("node_id, SUM(rx_bytes) AS rx, SUM(tx_bytes) AS tx").
		Where("recorded_at >= ?", monthStart).
		Group("node_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		traffic[r.NodeID] = [2]int64{r.Rx, r.Tx}
	}

	fraction := monthElapsedFraction(now)
	rep := &Report{ThresholdCents: threshold, Nodes: []NodeCost{}, Regions: []GroupCost{}, Isps: []GroupCost{}}
	byRegion := map[string]*GroupCost{}
	byISP := map[string]*GroupCost{}
	for _, n := range nodes {
		t := traffic[n.ID]
		var trafficCents int64
		if n.BillingType == "按流量" {
			trafficCents = int64(float64(t[0]+t[1]) / BytesPerGB * float64(n.TrafficPriceCents))
		}
		nc := NodeCost{
			ID: n.ID, Name: n.Name, Region: n.Region, ISP: n.ISP,
			BillingType: n.BillingType,
			MonthRxBytes: t[0], MonthTxBytes: t[1],
			TrafficCostCents: trafficCents,
			FixedCostCents:   n.MonthlyCostCents,
			ProjectedCents:   n.MonthlyCostCents + int64(float64(trafficCents)/fraction),
		}
		rep.Nodes = append(rep.Nodes, nc)
		group(byRegion, n.Region, nc)
		group(byISP, n.ISP, nc)
		sum := &rep.Summary
		sum.Nodes++
		sum.TrafficCents += nc.TrafficCostCents
		sum.FixedCents += nc.FixedCostCents
		sum.ProjectedCents += nc.ProjectedCents
	}
	rep.Regions = sortedGroups(byRegion)
	rep.Isps = sortedGroups(byISP)
	return rep, nil
}

// group 把节点成本计入维度汇总。
func group(by map[string]*GroupCost, key string, nc NodeCost) {
	if key == "" {
		key = "未知"
	}
	g, ok := by[key]
	if !ok {
		g = &GroupCost{Key: key}
		by[key] = g
	}
	g.Nodes++
	g.TrafficCents += nc.TrafficCostCents
	g.FixedCents += nc.FixedCostCents
	g.ProjectedCents += nc.ProjectedCents
}

// sortedGroups 汇总按预估降序（花销大头在前）。
func sortedGroups(by map[string]*GroupCost) []GroupCost {
	out := make([]GroupCost, 0, len(by))
	for _, g := range by {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProjectedCents > out[j].ProjectedCents })
	return out
}

// monthElapsedFraction 自然月已过比例（0-1]；首小时内不外推（取 1，
// 预估=月至今实际值，避免月初几小时预估爆炸）。
func monthElapsedFraction(now time.Time) float64 {
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	daysIn := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location()).Sub(monthStart).Hours() / 24
	elapsed := now.Sub(monthStart).Hours() / 24
	if elapsed < 1 {
		return 1
	}
	if f := elapsed / daysIn; f < 1 {
		return f
	}
	return 1
}

// Loop 周期执行高成本告警检查直到 ctx 取消；超阈值节点单发，回落自动消解。
func Loop(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = time.Hour
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := CheckAlerts(db, time.Now(), logger); err != nil {
			logger.Printf("cost check: %v", err)
		}
		t.Reset(interval)
	}
}

// CheckAlerts 高成本告警：按流量计费节点月至今流量花费超阈值 → 活跃告警
// 去重单发（重复只刷新消息）+ 通知；回落到阈值下自动消解。
// 包月/固定带宽节点不参与流量成本告警（沉没成本口径）。
func CheckAlerts(db *gorm.DB, now time.Time, logger *log.Logger) error {
	rep, err := Build(db, now)
	if err != nil {
		return err
	}
	n := notify.FromDB(db)
	for _, nc := range rep.Nodes {
		if nc.BillingType != "按流量" {
			continue
		}
		over := nc.TrafficCostCents > rep.ThresholdCents
		var active storage.Alert
		err := db.Where("node_id = ? AND kind = ? AND state = ?", nc.ID, AlertKindHighCost, alertActive).
			First(&active).Error
		switch {
		case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
			return err
		case over && errors.Is(err, gorm.ErrRecordNotFound):
			msg := fmt.Sprintf("本月流量花费 ¥%.2f 超阈值 ¥%.2f，建议切流或换低成本落地",
				float64(nc.TrafficCostCents)/100, float64(rep.ThresholdCents)/100)
			row := storage.Alert{NodeID: nc.ID, Kind: AlertKindHighCost, Severity: "warning", Message: msg, State: alertActive}
			if err := db.Create(&row).Error; err != nil {
				return err
			}
			logger.Printf("cost alert: node %d %s: %s", nc.ID, nc.Name, msg)
			if n.Enabled() {
				ev := notify.Event{
					Event: "cost.high",
					Text:  fmt.Sprintf("高成本：节点 %s %s", nc.Name, msg),
					Fields: map[string]any{
						"node_id": nc.ID, "traffic_cost_cents": nc.TrafficCostCents,
						"threshold_cents": rep.ThresholdCents,
					},
				}
				if err := n.Send(ev); err != nil {
					logger.Printf("notify webhook: %v", err)
				}
			}
		case over:
			msg := fmt.Sprintf("本月流量花费 ¥%.2f 超阈值 ¥%.2f，建议切流或换低成本落地",
				float64(nc.TrafficCostCents)/100, float64(rep.ThresholdCents)/100)
			active.Message = msg
			if err := db.Save(&active).Error; err != nil {
				return err
			}
		case !over && err == nil:
			now := time.Now()
			if err := db.Model(&storage.Alert{}).Where("id = ?", active.ID).
				Updates(map[string]any{"state": alertResolved, "resolved_at": now}).Error; err != nil {
				return err
			}
			logger.Printf("cost alert resolved: node %d %s 回落阈值下", nc.ID, nc.Name)
		}
	}
	return nil
}
