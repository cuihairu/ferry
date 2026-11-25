package cost

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cuihairu/ferry/packages/costref"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Cost reference (E-31) wiring: manually entered cost is the sole authority (alloc/dashboard accounting unchanged);
// reference prices only produce deviation hints and never change prices; first form = a public price table (JSON in
// settings, imported via the dashboard); online sources later = separate costref.Source implementations (check license first).

// SettingRefTable is the reference price table settings key (JSON []costref.Entry, empty = disabled).
const SettingRefTable = "costref_static_table"

// DefaultRefTolerancePct is the default deviation tolerance: |manual - quote| > 30% suggests manual review.
const DefaultRefTolerancePct = 30

// LoadRefSource reads the price table to build a source; returns (nil, nil) when none is imported.
func LoadRefSource(db *gorm.DB) (costref.Source, error) {
	data, ok, err := storage.GetSetting(db, SettingRefTable)
	if err != nil {
		return nil, err
	}
	if !ok || strings.TrimSpace(data) == "" {
		return nil, nil
	}
	return costref.ParseTable([]byte(data))
}

// SaveRefTable validates and persists (the import layer rejects dirty tables); an empty string clears and disables.
func SaveRefTable(db *gorm.DB, data []byte) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		return storage.SetSetting(db, SettingRefTable, "")
	}
	if _, err := costref.ParseTable(data); err != nil {
		return err
	}
	return storage.SetSetting(db, SettingRefTable, string(data))
}

// NodeQuery maps a node onto a price key: provider = datacenter (required), region, spec = plan tier
// "downstream bandwidth - monthly traffic". The mapping is a hint heuristic; node-side fields follow the dashboard.
func NodeQuery(n storage.Node) (costref.Query, error) {
	if strings.TrimSpace(n.Datacenter) == "" {
		return costref.Query{}, errors.New("node datacenter not set; cannot map price key")
	}
	return costref.Query{
		Provider: n.Datacenter,
		Region:   n.Region,
		Spec:     SpecOf(n.BwDownMbps, n.MonthlyTrafficQuotaBytes),
	}, nil
}

// SpecOf joins the plan tier string "<down>M-<monthly traffic>G", omitting zero segments; traffic uses decimal GB,
// the same unit as the traffic price (BytesPerGB).
func SpecOf(bwDownMbps int, quotaBytes int64) string {
	var parts []string
	if bwDownMbps > 0 {
		parts = append(parts, fmt.Sprintf("%dM", bwDownMbps))
	}
	if quotaBytes > 0 {
		parts = append(parts, fmt.Sprintf("%dG", quotaBytes/BytesPerGB))
	}
	return strings.Join(parts, "-")
}

// CheckNode reconciles one node: manually entered monthly fixed cost vs the quote (monthly fixed only;
// traffic price deviation hints come later with the price scan batch). Missing quote / currency mismatch /
// unset manual cost all return errors for the caller to surface facet by facet.
func CheckNode(src costref.Source, n storage.Node, tolerancePct int64) (costref.Quote, costref.Deviation, error) {
	q, err := NodeQuery(n)
	if err != nil {
		return costref.Quote{}, costref.Deviation{}, err
	}
	quote, err := src.Lookup(context.Background(), q)
	if err != nil {
		return costref.Quote{}, costref.Deviation{}, err
	}
	dev, err := costref.Compare(costref.Manual{MonthlyCents: n.MonthlyCostCents, Currency: n.Currency}, quote, tolerancePct)
	if err != nil {
		return quote, costref.Deviation{}, err
	}
	return quote, dev, nil
}
