package billing

import (
	"sort"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Anomaly classes (§4.5, F-0588~F-0590).
const (
	// AnomalyMissingBill flags a node that has estimates in the period but no
	// billed item at all (F-0588).
	AnomalyMissingBill = "missing_bill"
	// AnomalyUnmatchedResource flags a billed resource id that maps to no
	// known cloud resource (F-0589).
	AnomalyUnmatchedResource = "unmatched_resource"
	// AnomalySpike flags a period bucket whose actual spend exceeds the
	// previous bucket by more than the configured percentage (F-0590).
	AnomalySpike = "spend_spike"
)

// Anomaly is one detected gap between bills, resources and estimates.
type Anomaly struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	// Target names the subject: node id, resource id, or the period bucket.
	Target   string `json:"target"`
	Currency string `json:"currency"`
	// ActualCents / ExpectedCents frame the gap; SpikePct is set for
	// spend_spike.
	ActualCents   int64 `json:"actual_cents"`
	ExpectedCents int64 `json:"expected_cents"`
	SpikePct      int64 `json:"spike_pct"`
	// Detail is a short human-readable explanation.
	Detail string `json:"detail"`
}

// SpikeThresholdPct is the default growth percentage that flags a spike
// (F-0590): a bucket more than double the previous one.
const SpikeThresholdPct = 100

// DetectAnomalies scans the period [from,to] for the three anomaly classes
// (F-0588~F-0590). The returned rows are informational; nothing is mutated.
func DetectAnomalies(db *gorm.DB, from, to time.Time, spikePct int64) ([]Anomaly, error) {
	if spikePct <= 0 {
		spikePct = SpikeThresholdPct
	}
	out := []Anomaly{}

	// (1) Synced cloud resources with no billed item in the period (F-0588):
	// the resource exists on the vendor but no bill line covers it.
	var resIDs []string
	if err := db.Model(&storage.CloudResource{}).
		Select("external_id").Order("external_id ASC").Scan(&resIDs).Error; err != nil {
		return nil, err
	}
	billed := map[string]bool{}
	var billedIDs []string
	if err := db.Model(&storage.CostItem{}).
		Select("DISTINCT cost_items.resource_id AS resource_id").
		Joins("JOIN cost_records ON cost_records.id = cost_items.cost_record_id").
		Where("cost_items.resource_id <> ''").
		Where("cost_records.incurred_at >= ? AND cost_records.incurred_at <= ?", from, to).
		Scan(&billedIDs).Error; err != nil {
		return nil, err
	}
	for _, id := range billedIDs {
		billed[id] = true
	}
	for _, id := range resIDs {
		if billed[id] {
			continue
		}
		out = append(out, Anomaly{
			Kind: AnomalyMissingBill, Severity: "info",
			Target: id,
			Detail: "synced resource has no bill item in the period",
		})
	}

	// (2) Billed resource ids that match no synced cloud resource (F-0589).
	var items []struct {
		ResourceID string
		Currency   string
		Cents      int64
	}
	if err := db.Model(&storage.CostItem{}).
		Select("cost_items.resource_id AS resource_id, cost_records.currency AS currency, "+
			"SUM(cost_items.amount_cents) AS cents").
		Joins("JOIN cost_records ON cost_records.id = cost_items.cost_record_id").
		Where("cost_items.resource_id <> ''").
		Where("cost_records.incurred_at >= ? AND cost_records.incurred_at <= ?", from, to).
		Group("cost_items.resource_id, cost_records.currency").Scan(&items).Error; err != nil {
		return nil, err
	}
	if len(items) > 0 {
		var known []string
		if err := db.Model(&storage.CloudResource{}).
			Select("external_id").Scan(&known).Error; err != nil {
			return nil, err
		}
		knownSet := make(map[string]bool, len(known))
		for _, k := range known {
			knownSet[k] = true
		}
		for _, it := range items {
			if knownSet[it.ResourceID] {
				continue
			}
			out = append(out, Anomaly{
				Kind: AnomalyUnmatchedResource, Severity: "info",
				Target: it.ResourceID, Currency: it.Currency, ActualCents: it.Cents,
				Detail: "billed resource id has no synced cloud resource",
			})
		}
	}

	// (3) Month-over-month spikes (F-0590).
	trend, err := SpendTrend(db, "month", &from, &to)
	if err != nil {
		return nil, err
	}
	byCurrency := map[string][]TrendRow{}
	for _, r := range trend {
		byCurrency[r.Currency] = append(byCurrency[r.Currency], r)
	}
	for currency, rows := range byCurrency {
		sort.Slice(rows, func(i, j int) bool { return rows[i].Bucket < rows[j].Bucket })
		for i := 1; i < len(rows); i++ {
			prev, cur := rows[i-1].ActualCents, rows[i].ActualCents
			if prev <= 0 || cur <= 0 {
				continue
			}
			growth := (cur - prev) * 100 / prev
			if growth <= spikePct {
				continue
			}
			out = append(out, Anomaly{
				Kind: AnomalySpike, Severity: "warning",
				Target: rows[i].Bucket, Currency: currency,
				ActualCents: cur, ExpectedCents: prev, SpikePct: growth,
				Detail: "monthly spend grew sharply against the previous month",
			})
		}
	}
	return out, nil
}
