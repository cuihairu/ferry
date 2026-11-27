package cost

import (
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Cost anomalous-growth alerts (F-1831): the threshold pass (CheckAlerts)
// answers "already over budget"; this pass answers "burning abnormally
// fast" -- a traffic-billed node whose month-to-date daily traffic pace is a
// spike multiple of its trailing 7-day average. The floor and ratio reuse
// the traffic-anomaly tuning (F-1252); dedup is per node per month, so a bad
// month alerts once no matter how many checks re-fire. Fixed-billing nodes
// are excluded (sunk cost, no marginal growth to detect).
const (
	growthBaselineDays  = 7
	growthSpikeRatio    = 5
	growthBaselineFloor = int64(1) << 20 // 1 MiB/day: below this even 100x is noise
)

// CheckGrowth implements the growth-rate pass over the same traffic ledger
// the report builds from. Best effort: a query failure for the pass never
// fails the caller's other work.
func CheckGrowth(db *gorm.DB, now time.Time, logger *log.Logger) error {
	var nodes []storage.Node
	if err := db.Where("billing_type = ? AND traffic_price_cents > 0", "按流量").
		Find(&nodes).Error; err != nil {
		return err
	}
	if len(nodes) == 0 {
		return nil
	}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	// The pace side needs the full month-to-date, the baseline side the
	// trailing week: query from whichever starts earlier.
	windowStart := dayStart.AddDate(0, 0, -growthBaselineDays)
	if monthStart.Before(windowStart) {
		windowStart = monthStart
	}
	var logs []storage.NodeTrafficLog
	if err := db.Select("node_id", "rx_bytes", "tx_bytes", "recorded_at").
		Where("recorded_at >= ?", windowStart).
		Find(&logs).Error; err != nil {
		return err
	}
	daily := map[uint]map[string]int64{} // node -> day -> bytes
	for _, l := range logs {
		if daily[l.NodeID] == nil {
			daily[l.NodeID] = map[string]int64{}
		}
		daily[l.NodeID][l.RecordedAt.Format("2006-01-02")] += l.RxBytes + l.TxBytes
	}
	daysElapsed := int(now.Sub(monthStart).Hours()/24) + 1
	monthKey := monthStart.Format("2006-01-02")
	for i := range nodes {
		n := &nodes[i]
		sums := daily[n.ID]
		var baseline int64
		for d := 1; d <= growthBaselineDays; d++ {
			baseline += sums[dayStart.AddDate(0, 0, -d).Format("2006-01-02")]
		}
		baseline /= growthBaselineDays
		if baseline < growthBaselineFloor {
			continue // a tiny baseline says nothing about pace
		}
		var month int64
		for day, v := range sums {
			if day >= monthKey {
				month += v
			}
		}
		pace := month / int64(daysElapsed)
		if pace < growthSpikeRatio*baseline {
			continue
		}
		dedup := fmt.Sprintf("node:%d:cost_growth:%s", n.ID, now.Format("200601"))
		var cnt int64
		if err := db.Model(&storage.Event{}).
			Where("kind = ? AND dedup_key = ?", herald.KindCostGrowth, dedup).
			Count(&cnt).Error; err != nil {
			return err
		}
		if cnt > 0 {
			continue
		}
		paceCents := int64(float64(pace) / BytesPerGB * float64(n.TrafficPriceCents))
		baseCents := int64(float64(baseline) / BytesPerGB * float64(n.TrafficPriceCents))
		msg := fmt.Sprintf("本月日均流量花费 ¥%.2f，已达近 %d 日均值（¥%.2f）的 %d 倍以上，成本异常增长",
			float64(paceCents)/100, growthBaselineDays, float64(baseCents)/100, growthSpikeRatio)
		logger.Printf("cost growth: node %d %s: %s", n.ID, n.Name, msg)
		if _, err := herald.Emit(db, herald.EmitInput{
			Kind: herald.KindCostGrowth, Severity: herald.SeverityWarning,
			Title:    fmt.Sprintf("节点 %s 成本异常增长", n.Name),
			Body:     msg,
			Target:   herald.TargetAdmin,
			DedupKey: dedup,
			Meta: map[string]any{
				"node_id": n.ID, "pace_bytes": pace, "baseline_bytes": baseline,
				"ratio": growthSpikeRatio, "baseline_days": growthBaselineDays,
			},
		}); err != nil {
			logger.Printf("herald emit cost_growth: node=%d %v", n.ID, err)
		}
	}
	return nil
}
