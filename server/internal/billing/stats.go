package billing

import (
	"fmt"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Spend scopes (§4.5, F-0581~F-0586). Each scope sums the money table that
// carries its attribution: records for provider/account/region, line items for
// resources, allocations for nodes, estimates for paths. Amounts are summed
// per currency and never mixed (F-0594): one row per (group, currency).
func ValidSpendScope(s string) bool {
	switch s {
	case TargetProvider, TargetAccount, TargetRegion, TargetResource, TargetNode, TargetPath:
		return true
	}
	return false
}

// SpendRow is one aggregate bucket: a scope key with its per-currency sums.
type SpendRow struct {
	// Key is the free-form group key (account name, region, resource id…).
	Key string `json:"key"`
	// TargetID is the row id when the group has one (provider/node/path).
	TargetID uint   `json:"target_id,omitempty"`
	Name     string `json:"name,omitempty"`
	// Currency of this row's sums; rows are never merged across currencies
	// (F-0594).
	Currency string `json:"currency"`
	// ActualCents is the vendor-billed sum (provider/account/region/resource
	// scopes), AllocatedCents the node-allocated sum (F-0585), EstimateCents
	// the estimate sum for paths (F-0586).
	ActualCents    int64 `json:"actual_cents"`
	AllocatedCents int64 `json:"allocated_cents"`
	EstimateCents  int64 `json:"estimate_cents"`
	// ReportCents sums the FX-converted report totals carried by records
	// (F-0595); zero when no rate was supplied at import.
	ReportCents int64 `json:"report_cents"`
	// Count is the number of source rows behind the sums.
	Count int64 `json:"count"`
}

// SpendByScope aggregates the requested scope over the period. Empty from/to
// means unbounded.
func SpendByScope(db *gorm.DB, scope string, from, to *time.Time) ([]SpendRow, error) {
	switch scope {
	case TargetProvider:
		return spendRecords(db, "cost_provider_id", from, to, providerNames(db))
	case TargetAccount:
		return spendRecords(db, "account", from, to, nil)
	case TargetRegion:
		return spendRecords(db, "region", from, to, nil)
	case TargetResource:
		return spendItems(db, from, to)
	case TargetNode:
		return spendAllocatedNodes(db, from, to)
	case TargetPath:
		return spendPathEstimates(db, from, to)
	}
	return nil, fmt.Errorf("invalid spend scope %q", scope)
}

func spendRecords(db *gorm.DB, groupCol string, from, to *time.Time, names map[uint]string) ([]SpendRow, error) {
	// groupCol is an internal column constant; cast to TEXT so one scan shape
	// covers numeric (provider id) and free-form (account/region) groups.
	q := db.Model(&storage.CostRecord{}).
		Select("CAST(" + groupCol + " AS TEXT) AS grp, currency, SUM(amount_cents) AS cents, " +
			"SUM(report_amount_cents) AS report_cents, COUNT(*) AS cnt").
		Group(groupCol + ", currency")
	q = boundPeriod(q, "incurred_at", from, to)
	var rows []struct {
		Grp         string
		Currency    string
		Cents       int64
		ReportCents int64
		Cnt         int64
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]SpendRow, 0, len(rows))
	for _, r := range rows {
		row := SpendRow{Key: r.Grp, Currency: r.Currency, ActualCents: r.Cents,
			ReportCents: r.ReportCents, Count: r.Cnt}
		if names != nil {
			if id, err := strconv.ParseUint(r.Grp, 10, 64); err == nil {
				row.TargetID = uint(id)
				row.Name = names[uint(id)]
				row.Key = ""
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func spendItems(db *gorm.DB, from, to *time.Time) ([]SpendRow, error) {
	q := db.Model(&storage.CostItem{}).
		Select("resource_id AS grp, cost_items.region AS region, cost_records.currency AS currency, " +
			"SUM(cost_items.amount_cents) AS cents, COUNT(*) AS cnt").
		Joins("JOIN cost_records ON cost_records.id = cost_items.cost_record_id").
		Where("cost_items.resource_id <> ''").
		Group("cost_items.resource_id, cost_items.region, cost_records.currency")
	boundPeriodItem(q, from, to)
	var rows []struct {
		Grp      string
		Region   string
		Currency string
		Cents    int64
		Cnt      int64
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]SpendRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, SpendRow{
			Key: r.Grp, Name: r.Region, Currency: r.Currency,
			ActualCents: r.Cents, Count: r.Cnt,
		})
	}
	return out, nil
}

func spendAllocatedNodes(db *gorm.DB, from, to *time.Time) ([]SpendRow, error) {
	// Allocations carry no currency of their own; the join recovers their
	// record's currency so sums stay per-currency (F-0594).
	q := db.Model(&storage.CostAllocation{}).
		Select("cost_allocations.target_id AS grp, cost_records.currency AS currency, "+
			"SUM(cost_allocations.allocated_cents) AS cents, COUNT(*) AS cnt").
		Joins("JOIN cost_items ON cost_items.id = cost_allocations.cost_item_id").
		Joins("JOIN cost_records ON cost_records.id = cost_items.cost_record_id").
		Where("cost_allocations.target_kind = ?", TargetNode).
		Group("cost_allocations.target_id, cost_records.currency")
	boundPeriod(q, "cost_records.incurred_at", from, to)
	var rows []struct {
		Grp      int64
		Currency string
		Cents    int64
		Cnt      int64
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	var nodes []storage.Node
	_ = db.Find(&nodes).Error
	byID := map[uint]string{}
	for _, n := range nodes {
		byID[n.ID] = n.Name
	}
	out := make([]SpendRow, 0, len(rows))
	for _, r := range rows {
		id := uint(r.Grp)
		out = append(out, SpendRow{
			TargetID: id, Name: byID[id], Currency: r.Currency,
			AllocatedCents: r.Cents, Count: r.Cnt,
		})
	}
	return out, nil
}

func spendPathEstimates(db *gorm.DB, from, to *time.Time) ([]SpendRow, error) {
	q := db.Model(&storage.CostEstimate{}).
		Select("path_id AS grp, currency, SUM(amount_cents) AS cents, COUNT(*) AS cnt").
		Where("path_id IS NOT NULL AND unknown_price = ?", false).
		Group("path_id, currency")
	if from != nil {
		q = q.Where("period_start >= ?", *from)
	}
	if to != nil {
		q = q.Where("period_end <= ?", *to)
	}
	var rows []struct {
		Grp      int64
		Currency string
		Cents    int64
		Cnt      int64
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]SpendRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, SpendRow{
			TargetID: uint(r.Grp), Currency: r.Currency,
			EstimateCents: r.Cents, Count: r.Cnt,
		})
	}
	return out, nil
}

func boundPeriod(q *gorm.DB, col string, from, to *time.Time) *gorm.DB {
	if from != nil {
		q = q.Where(col+" >= ?", *from)
	}
	if to != nil {
		q = q.Where(col+" <= ?", *to)
	}
	return q
}

func boundPeriodItem(q *gorm.DB, from, to *time.Time) {
	if from != nil {
		q = q.Where("cost_records.incurred_at >= ?", *from)
	}
	if to != nil {
		q = q.Where("cost_records.incurred_at <= ?", *to)
	}
}

func providerNames(db *gorm.DB) map[uint]string {
	var rows []storage.CostProvider
	_ = db.Find(&rows).Error
	out := map[uint]string{}
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out
}

// TrendRow is one bucket of the period trend (F-0593): a day, a month, or one
// billing cycle with its per-currency actual sum.
type TrendRow struct {
	// Bucket is the bucket start (day/month) or the cycle "from/to" pair.
	Bucket      string `json:"bucket"`
	Currency    string `json:"currency"`
	ActualCents int64  `json:"actual_cents"`
	Count       int64  `json:"count"`
}

// SpendTrend buckets actual spend per period (F-0593): bucket is "day",
// "month" or "cycle" (the vendor's own billing period pair).
func SpendTrend(db *gorm.DB, bucket string, from, to *time.Time) ([]TrendRow, error) {
	var recs []storage.CostRecord
	q := db.Model(&storage.CostRecord{}).Order("incurred_at ASC")
	q = boundPeriod(q, "incurred_at", from, to)
	if err := q.Find(&recs).Error; err != nil {
		return nil, err
	}
	format := ""
	switch bucket {
	case "day":
		format = "2006-01-02"
	case "month":
		format = "2006-01"
	case "cycle":
	default:
		return nil, fmt.Errorf("invalid bucket %q", bucket)
	}
	out := []TrendRow{}
	idx := map[string]int{}
	for _, r := range recs {
		key := ""
		switch bucket {
		case "cycle":
			key = fmt.Sprintf("%s/%s", r.PeriodStart.Format("2006-01-02"), r.PeriodEnd.Format("2006-01-02"))
		default:
			key = r.IncurredAt.Format(format)
		}
		id := key + "|" + r.Currency
		i, ok := idx[id]
		if !ok {
			out = append(out, TrendRow{Bucket: key, Currency: r.Currency})
			i = len(out) - 1
			idx[id] = i
		}
		out[i].ActualCents += r.AmountCents
		out[i].Count++
	}
	return out, nil
}
