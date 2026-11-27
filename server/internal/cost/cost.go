// Package cost: cost dashboard and high-cost alerts (E-23): node traffic spend
// (only traffic-billed nodes carry marginal cost), region/ISP rollups, monthly
// projection (month-to-date, pro-rated by day); over-threshold nodes get a
// single alert; fixed cost is sunk, summary-only, not in allocation (same accounting as alloc).
package cost

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Accounting constants.
const (
	// BytesPerGB is decimal GB (same unit as the traffic price in cents/GB).
	BytesPerGB = 1_000_000_000
	// settingThreshold is the persistence key for the threshold (cents/month).
	settingThreshold = "cost_alert_threshold_cents"
	// DefaultThresholdCents is the default threshold: ¥50/month.
	DefaultThresholdCents = 5000
	// AlertKindHighCost is the high-cost alert kind (reuses the alerts table).
	AlertKindHighCost = "high_cost"
)

// Alert state values (consistent with the handler-side alerts table).
const (
	alertActive   = "active"
	alertResolved = "resolved"
)

// NodeCost is the monthly cost view of one node.
type NodeCost struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Region       string `json:"region"`
	ISP          string `json:"isp"`
	BillingType  string `json:"billing_type"`
	MonthRxBytes int64  `json:"month_rx_bytes"`
	MonthTxBytes int64  `json:"month_tx_bytes"`
	// TrafficCostCents is month-to-date traffic spend in cents (nonzero only for traffic-billed nodes).
	TrafficCostCents int64 `json:"traffic_cost_cents"`
	// FixedCostCents is the monthly fixed cost in cents (monthly/flat-bandwidth billing).
	FixedCostCents int64 `json:"fixed_cost_cents"`
	// ProjectedCents is the monthly projection in cents: full-month fixed + traffic pro-rated by day.
	ProjectedCents int64 `json:"projected_cents"`
}

// GroupCost is a cost summary along the region/ISP dimension, with the
// dimension's capacity (F-1944): CapacityMbps sums Node.CapacityMbps() over
// the group's enabled nodes (the deliverable bandwidth a region can take),
// EnabledNodes counts them; disabled nodes cost but carry no capacity.
type GroupCost struct {
	Key            string `json:"key"`
	Nodes          int    `json:"nodes"`
	EnabledNodes   int    `json:"enabled_nodes"`
	CapacityMbps   int64  `json:"capacity_mbps"`
	TrafficCents   int64  `json:"traffic_cents"`
	FixedCents     int64  `json:"fixed_cents"`
	ProjectedCents int64  `json:"projected_cents"`
}

// Report is the cost dashboard data.
type Report struct {
	Nodes          []NodeCost  `json:"nodes"`
	Regions        []GroupCost `json:"regions"`
	Isps           []GroupCost `json:"isps"`
	Summary        GroupCost   `json:"summary"`
	ThresholdCents int64       `json:"threshold_cents"`
}

// GetThreshold reads the alert threshold (cents/month); unset or dirty data falls back to the default.
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

// SaveThreshold validates and persists the alert threshold (cents/month).
func SaveThreshold(db *gorm.DB, cents int64) error {
	if cents < 0 {
		return errors.New("threshold must be >= 0")
	}
	return storage.SetSetting(db, settingThreshold, fmt.Sprintf("%d", cents))
}

// Build aggregates month-to-date traffic and cost per node for the calendar month, grouped by region/ISP.
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
			BillingType:  n.BillingType,
			MonthRxBytes: t[0], MonthTxBytes: t[1],
			TrafficCostCents: trafficCents,
			FixedCostCents:   n.MonthlyCostCents,
			ProjectedCents:   n.MonthlyCostCents + int64(float64(trafficCents)/fraction),
		}
		rep.Nodes = append(rep.Nodes, nc)
		cap := n.CapacityMbps()
		group(byRegion, n.Region, nc, cap, n.Enabled)
		group(byISP, n.ISP, nc, cap, n.Enabled)
		sum := &rep.Summary
		sum.Nodes++
		if n.Enabled {
			sum.EnabledNodes++
			sum.CapacityMbps += int64(cap)
		}
		sum.TrafficCents += nc.TrafficCostCents
		sum.FixedCents += nc.FixedCostCents
		sum.ProjectedCents += nc.ProjectedCents
	}
	rep.Regions = sortedGroups(byRegion)
	rep.Isps = sortedGroups(byISP)
	return rep, nil
}

// group adds a node's cost into a dimension summary; capacity and the enabled
// count only take enabled nodes (a paused node delivers no bandwidth).
func group(by map[string]*GroupCost, key string, nc NodeCost, capacityMbps int, enabled bool) {
	if key == "" {
		key = "未知"
	}
	g, ok := by[key]
	if !ok {
		g = &GroupCost{Key: key}
		by[key] = g
	}
	g.Nodes++
	if enabled {
		g.EnabledNodes++
		g.CapacityMbps += int64(capacityMbps)
	}
	g.TrafficCents += nc.TrafficCostCents
	g.FixedCents += nc.FixedCostCents
	g.ProjectedCents += nc.ProjectedCents
}

// sortedGroups sorts summaries by projected cost descending (biggest spenders first).
func sortedGroups(by map[string]*GroupCost) []GroupCost {
	out := make([]GroupCost, 0, len(by))
	for _, g := range by {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProjectedCents > out[j].ProjectedCents })
	return out
}

// monthElapsedFraction returns the elapsed fraction of the calendar month (0-1];
// no extrapolation in the first hour (use 1 so the projection equals month-to-date actual).
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

// Loop runs the high-cost alert check on interval until ctx is cancelled; over-threshold
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
		if err := CheckGrowth(db, time.Now(), logger); err != nil {
			logger.Printf("cost growth check: %v", err)
		}
		t.Reset(interval)
	}
}

// CheckAlerts implements high-cost alerts: a traffic-billed node over the threshold on
// month-to-date traffic spend fires one deduplicated alert (repeats only refresh the
// message) plus a notification, auto-resolving below threshold; fixed-billing nodes are excluded (sunk cost).
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
			// Emit a herald event as soon as the alert activates (HERALD-3 slack): the dedup
			// gate still applies, repeats only refresh the message; failures log only.
			if _, err := herald.Emit(db, herald.EmitInput{
				Kind: herald.KindCostExceeded, Severity: herald.SeverityWarning,
				Title:    fmt.Sprintf("节点 %s 流量花费超阈值", nc.Name),
				Body:     msg,
				Target:   herald.TargetAdmin,
				DedupKey: fmt.Sprintf("node:%d:cost_exceeded", nc.ID),
				Meta:     map[string]any{"node_id": nc.ID, "traffic_cost_cents": nc.TrafficCostCents, "threshold_cents": rep.ThresholdCents},
			}); err != nil {
				logger.Printf("herald emit cost_exceeded: node=%d %v", nc.ID, err)
			}
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
