package billing

import (
	"fmt"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Estimate price-source markers: where each computed estimate got its unit
// price, so every row is traceable back to the hand-entered input (F-0550).
const (
	priceNodeMonthly = "node:monthly_cost_cents"
	priceNodeTraffic = "node:traffic_price_cents"
	// BytesPerGB is decimal GB, the same traffic unit the dashboard prices.
	BytesPerGB = 1_000_000_000
)

// lineKinds are the cost-line classes the estimate engine consumes
// (F-0542/F-0545~F-0549): everything except the base instance/traffic kinds,
// which are computed from the node's own billing fields.
func ValidLineKind(k string) bool {
	switch k {
	case KindHourly, KindBandwidth, KindIP, KindDisk, KindSnapshot,
		KindBackup, KindDiscount, KindCustom:
		return true
	}
	return false
}

// EstimateNode computes the estimate rows of one node for [from,to] (F-0542~
// F-0549): the base billing kind from the node fields plus one row per enabled
// cost line, upserted by the estimate dedupe key. Rows for a price nobody
// filled in are stored marked unknown, never invented (F-0551). Existing rows
// for the same scope/kind/period are refreshed in place — estimates are
// recomputable and never billed.
func EstimateNode(db *gorm.DB, node *storage.Node, from, to time.Time) ([]storage.CostEstimate, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("period end %v before start %v", to, from)
	}
	currency := node.Currency
	if currency == "" {
		currency = "CNY"
	}
	out := []storage.CostEstimate{}

	// Base billing (design 套餐与成本 §2): monthly/flat-bandwidth enter as the
	// fixed instance cost; traffic-billed nodes estimate from booked traffic.
	switch node.BillingType {
	case KindTraffic, "按流量":
		amount, known := periodTrafficCents(db, node, from, to)
		row, err := upsertEstimate(db, estimateRow(node, KindTraffic, from, to, currency),
			map[string]any{"amount_cents": amount, "unknown_price": !known,
				"price_source": priceNodeTraffic})
		if err != nil {
			return nil, err
		}
		out = append(out, *row)
	default:
		// Monthly/flat-bandwidth cost prorates into the requested period the
		// same way the cost lines do; a full-month period is exactly 1.
		amount := int64(float64(node.MonthlyCostCents) * periodFractionOfMonth(from, to))
		known := node.MonthlyCostCents > 0
		row, err := upsertEstimate(db, estimateRow(node, KindInstance, from, to, currency),
			map[string]any{"amount_cents": amount, "unknown_price": !known,
				"price_source": priceNodeMonthly})
		if err != nil {
			return nil, err
		}
		out = append(out, *row)
	}

	// Cost lines (F-0542/F-0545~F-0549): hourly lines compute price × hours,
	// the rest prorate their monthly amount into the period.
	var lines []storage.NodeCostLine
	if err := db.Where("node_id = ? AND enabled = ?", node.ID, true).Order("id ASC").Find(&lines).Error; err != nil {
		return nil, err
	}
	for _, line := range lines {
		amount, known := lineAmount(line, from, to)
		row, err := upsertEstimate(db, estimateRow(node, line.Kind, from, to, line.Currency),
			map[string]any{"amount_cents": amount, "unknown_price": !known,
				"price_source": fmt.Sprintf("line:%d", line.ID)})
		if err != nil {
			return nil, err
		}
		out = append(out, *row)
	}
	return out, nil
}

// EstimateAll runs EstimateNode for every node; it returns the number of
// estimate rows written.
func EstimateAll(db *gorm.DB, from, to time.Time) (int, error) {
	var nodes []storage.Node
	if err := db.Find(&nodes).Error; err != nil {
		return 0, err
	}
	count := 0
	for i := range nodes {
		rows, err := EstimateNode(db, &nodes[i], from, to)
		if err != nil {
			return count, fmt.Errorf("node %d: %w", nodes[i].ID, err)
		}
		count += len(rows)
	}
	return count, nil
}

func estimateRow(node *storage.Node, kind string, from, to time.Time, currency string) storage.CostEstimate {
	return storage.CostEstimate{
		NodeID: &node.ID, Region: node.Region, Kind: kind, Currency: currency,
		PeriodStart: from, PeriodEnd: to,
		DedupeKey: EstimateKey(kind, &node.ID, nil, node.Region, from, to),
	}
}

// upsertEstimate refreshes the estimate row behind one dedupe key: the first
// computation creates it, a recomputation updates the amounts in place.
func upsertEstimate(db *gorm.DB, row storage.CostEstimate, updates map[string]any) (*storage.CostEstimate, error) {
	var existing storage.CostEstimate
	err := db.Where("dedupe_key = ?", row.DedupeKey).First(&existing).Error
	if err == nil {
		if err := db.Model(&existing).Updates(updates).Error; err != nil {
			return nil, err
		}
		if err := db.First(&existing, existing.ID).Error; err != nil {
			return nil, err
		}
		return &existing, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	for k, v := range updates {
		if k == "amount_cents" {
			row.AmountCents = v.(int64)
		} else if k == "unknown_price" {
			row.UnknownPrice = v.(bool)
		} else if k == "price_source" {
			row.PriceSource = v.(string)
		}
	}
	if err := db.Create(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// lineAmount resolves one cost line's amount over the period: hourly lines are
// price × hours (F-0542), monthly lines prorate by the period's share of its
// calendar month; an unknown price yields the zero sentinel (F-0551) and a
// discount line keeps its negative sign (F-0548).
func lineAmount(line storage.NodeCostLine, from, to time.Time) (int64, bool) {
	if line.UnknownPrice {
		return 0, false
	}
	if line.Kind == KindHourly {
		if line.HourlyPriceCents == 0 {
			return 0, false
		}
		return int64(float64(line.HourlyPriceCents) * to.Sub(from).Hours()), true
	}
	if line.AmountCents == 0 {
		return 0, false
	}
	fraction := periodFractionOfMonth(from, to)
	return int64(float64(line.AmountCents) * fraction), true
}

// periodFractionOfMonth is the period's share of the calendar month containing
// its start; a full-month period is exactly 1.
func periodFractionOfMonth(from, to time.Time) float64 {
	days := to.Sub(from).Hours() / 24
	if days <= 0 {
		return 0
	}
	y, m, _ := from.Date()
	monthDays := time.Date(y, m+1, 1, 0, 0, 0, 0, from.Location()).
		Sub(time.Date(y, m, 1, 0, 0, 0, 0, from.Location())).Hours() / 24
	f := days / monthDays
	if f > 1 {
		f = 1
	}
	return f
}

// periodTrafficCents sums the traffic booked in the period and prices it
// (same 手录单价 authority as the dashboard, F-0543); a node without a price
// yields the unknown sentinel.
func periodTrafficCents(db *gorm.DB, node *storage.Node, from, to time.Time) (int64, bool) {
	if node.TrafficPriceCents <= 0 {
		return 0, false
	}
	var sum int64
	if err := db.Model(&storage.NodeTrafficLog{}).
		Select("COALESCE(SUM(rx_bytes + tx_bytes),0)").
		Where("node_id = ? AND recorded_at >= ? AND recorded_at <= ?", node.ID, from, to).
		Scan(&sum).Error; err != nil {
		return 0, false
	}
	return int64(float64(sum) / float64(BytesPerGB) * float64(node.TrafficPriceCents)), true
}
