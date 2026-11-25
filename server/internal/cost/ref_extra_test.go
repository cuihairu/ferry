package cost

import (
	"testing"

	"github.com/cuihairu/ferry/packages/costref"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSaveRefTableEdge covers SaveRefTable's empty-string clear and dirty-table rejection branches.
func TestSaveRefTableEdge(t *testing.T) {
	db := newTestDB(t)
	// Empty string: writes an empty value (the current implementation expresses clearing as SetSetting("")).
	if err := SaveRefTable(db, []byte("   ")); err != nil {
		t.Fatalf("clear: %v", err)
	}
	// Dirty table is rejected.
	if err := SaveRefTable(db, []byte(`{"not":"a-table"}`)); err == nil {
		t.Fatal("bad table should error")
	}
	// Good table is persisted.
	good := []byte(`[{"provider":"vultr","region":"tokyo","spec":"500M-2G","monthly_cents":10000,"currency":"CNY"}]`)
	if err := SaveRefTable(db, good); err != nil {
		t.Fatalf("save good: %v", err)
	}
	if v, ok, _ := storage.GetSetting(db, SettingRefTable); !ok || v == "" {
		t.Fatal("setting missing after save")
	}
	// Clear again: value is the empty string (expresses disabled).
	if err := SaveRefTable(db, nil); err != nil {
		t.Fatalf("clear nil: %v", err)
	}
	if v, _, _ := storage.GetSetting(db, SettingRefTable); v != "" {
		t.Fatalf("clear should blank value, got %q", v)
	}
}

// TestCheckNodeLookupError covers CheckNode's error branch when the quote lookup fails.
func TestCheckNodeLookupError(t *testing.T) {
	src := costref.NewStatic("pricelist", nil) // empty table → ErrNotFound
	n := storage.Node{Datacenter: "vultr", Region: "tokyo",
		BwDownMbps: 500, MonthlyTrafficQuotaBytes: 2 * BytesPerGB,
		MonthlyCostCents: 10000, Currency: "CNY"}
	if _, _, err := CheckNode(src, n, 30); err == nil {
		t.Fatal("want error for missing quote")
	}
}
