package billing

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPathCostExplain covers F-0603/F-0604/F-0610: a fully priced fresh path is
// Precise with per-leg explain lines; an unknown metered price or a stale price
// marks its leg Unknown and drops Precise instead of inventing amounts.
func TestPathCostExplain(t *testing.T) {
	db := openBillingDB(t)
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	entry := storage.Node{Name: "pe", Token: "tk-pe", Role: "entry", BillingType: "按流量",
		TrafficPriceCents: 30, Currency: "CNY"}
	landing := storage.Node{Name: "pl", Token: "tk-pl", Role: "landing", BillingType: "包月",
		MonthlyCostCents: 3100, Currency: "CNY"}
	relay := storage.Node{Name: "pr", Token: "tk-pr", Role: "landing", BillingType: "按流量",
		TrafficPriceCents: 0, Currency: "CNY"} // metered but unpriced
	for _, n := range []*storage.Node{&entry, &landing, &relay} {
		if err := db.Create(n).Error; err != nil {
			t.Fatalf("node %s: %v", n.Name, err)
		}
	}
	// Pin freshness: entry/landing fresh (today), relay stale (9 days ago). GORM
	// stamps updated_at itself on Create, so the pin goes through UpdateColumn.
	pin := func(id uint, at time.Time) {
		if err := db.Model(&storage.Node{}).Where("id = ?", id).
			UpdateColumn("updated_at", at).Error; err != nil {
			t.Fatalf("pin updated_at: %v", err)
		}
	}
	pin(entry.ID, now.Add(-1*time.Hour))
	pin(landing.ID, now.Add(-2*time.Hour))
	pin(relay.ID, now.Add(-9*24*time.Hour))

	// Period traffic on the entry: 3 GB x 30 cents = 90.
	if err := db.Create(&storage.NodeTrafficLog{NodeID: entry.ID, Proc: "xray",
		RxBytes: BytesPerGB, TxBytes: 2 * BytesPerGB, RecordedAt: now.Add(-24 * time.Hour)}).Error; err != nil {
		t.Fatalf("traffic: %v", err)
	}

	path := storage.Path{Name: "p-explain", EntryNodeID: entry.ID, LandingNodeID: landing.ID,
		RelayNodeID: &relay.ID, Direction: "out", Enabled: true}
	if err := db.Create(&path).Error; err != nil {
		t.Fatalf("path: %v", err)
	}

	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	pc, err := PathCostEstimate(db, path, from, to, now)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if len(pc.Explain) != 3 {
		t.Fatalf("explain legs = %d, want 3", len(pc.Explain))
	}
	byRole := map[string]LegCost{}
	for _, leg := range pc.Explain {
		byRole[leg.Role] = leg
	}
	if e := byRole["entry"]; e.CentsPerGB != 30 || !e.Known || !e.Fresh || e.Source != priceNodeTraffic {
		t.Fatalf("entry leg: %+v", e)
	}
	if l := byRole["landing"]; l.Unit != "fixed" || l.MonthlyCents != 3100 || !l.Known {
		t.Fatalf("landing leg: %+v", l)
	}
	if r := byRole["relay"]; !r.Unknown {
		t.Fatalf("unpriced stale metered leg must be unknown: %+v", r)
	}
	if pc.MarginalPerGBCents != 30 {
		t.Fatalf("marginal = %d, want 30", pc.MarginalPerGBCents)
	}
	// The relay's unknown leg drops Precise (F-0610) but not the computable sum:
	// 3 GB x 30 = 90 metered + October's fixed 3100.
	if pc.Precise {
		t.Fatal("path with an unknown/stale leg must not claim precise")
	}
	if pc.TotalCents != 90+3100 {
		t.Fatalf("total = %d, want 3190", pc.TotalCents)
	}

	// Listing covers the enabled path.
	rows, err := PathCostEstimates(db, from, to, now)
	if err != nil || len(rows) != 1 || rows[0].PathID != path.ID {
		t.Fatalf("listing: rows=%d err=%v", len(rows), err)
	}
}

// TestPathCostPreciseFresh covers the all-fresh-priced case (F-0610): the
// estimate is Precise and the fixed legs prorate over the partial month.
func TestPathCostPreciseFresh(t *testing.T) {
	db := openBillingDB(t)
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	entry := storage.Node{Name: "pf-e", Token: "tk-pf-e", Role: "entry", BillingType: "包月",
		MonthlyCostCents: 1500, Currency: "CNY"}
	landing := storage.Node{Name: "pf-l", Token: "tk-pf-l", Role: "landing", BillingType: "包月",
		MonthlyCostCents: 1500, Currency: "CNY"}
	for _, n := range []*storage.Node{&entry, &landing} {
		if err := db.Create(n).Error; err != nil {
			t.Fatalf("node %s: %v", n.Name, err)
		}
		if err := db.Model(&storage.Node{}).Where("id = ?", n.ID).
			UpdateColumn("updated_at", now.Add(-time.Hour)).Error; err != nil {
			t.Fatalf("pin: %v", err)
		}
	}
	path := storage.Path{Name: "p-fresh", EntryNodeID: entry.ID, LandingNodeID: landing.ID,
		Direction: "in", Enabled: true}
	if err := db.Create(&path).Error; err != nil {
		t.Fatalf("path: %v", err)
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	pc, err := PathCostEstimate(db, path, from, to, now)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if !pc.Precise {
		t.Fatal("fresh priced path must be precise")
	}
	// Two legs x 1500 prorated over 15/31 of October, each leg rounding alone.
	frac := periodFractionOfMonth(from, to)
	want := 2 * int64(float64(1500)*frac)
	if pc.TotalCents != want {
		t.Fatalf("total = %d, want %d", pc.TotalCents, want)
	}
}
