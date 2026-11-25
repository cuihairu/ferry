package costref

import (
	"context"
	"errors"
	"testing"
)

func staticTestSource(t *testing.T) *StaticSource {
	t.Helper()
	s, err := ParseTable([]byte(`[
		{"provider":" Vultr ","region":"Tokyo","spec":"1c512m","monthly_cents":600,"traffic_price":100,"url":"https://example.com/vultr"},
		{"provider":"vultr","region":"tokyo","spec":"2c1g","monthly_cents":1200,"currency":"USD"},
		{"provider":"搬瓦工","region":"HK","spec":"1c512m-100m","monthly_cents":5000}
	]`))
	if err != nil {
		t.Fatalf("ParseTable: %v", err)
	}
	return s
}

func TestStaticLookup(t *testing.T) {
	s := staticTestSource(t)
	ctx := context.Background()

	// Hit: case and surrounding whitespace normalized.
	q, err := s.Lookup(ctx, Query{Provider: "vultr", Region: "TOKYO", Spec: "1c512m"})
	if err != nil {
		t.Fatalf("Lookup hit: %v", err)
	}
	if q.MonthlyCents != 600 || q.TrafficPrice != 100 || q.Currency != "CNY" || q.URL == "" {
		t.Fatalf("quote fields = %+v", q)
	}
	if s.Name() != "pricelist" {
		t.Fatalf("Name = %q", s.Name())
	}

	// After the default-currency backfill, the row carrying no currency passes USD through unchanged.
	q, err = s.Lookup(ctx, Query{Provider: "VULTR", Region: "Tokyo", Spec: "2c1g"})
	if err != nil || q.Currency != "USD" || q.MonthlyCents != 1200 {
		t.Fatalf("second row: quote=%+v err=%v", q, err)
	}

	// Miss.
	if _, err := s.Lookup(ctx, Query{Provider: "vultr", Region: "LA", Spec: "1c512m"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("miss err = %v", err)
	}
}

func TestParseTableRejects(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"bad json", `[{]`},
		{"not array", `{"provider":"x"}`},
		{"missing spec", `[{"provider":"vultr","monthly_cents":600}]`},
		{"missing provider", `[{"spec":"1c512m","monthly_cents":600}]`},
		{"zero price", `[{"provider":"vultr","spec":"1c","monthly_cents":0}]`},
		{"negative price", `[{"provider":"vultr","spec":"1c","monthly_cents":-5}]`},
	}
	for _, c := range cases {
		if _, err := ParseTable([]byte(c.data)); err == nil {
			t.Fatalf("%s: want error", c.name)
		}
	}
}

func TestCompare(t *testing.T) {
	quote := Quote{MonthlyCents: 1000, Currency: "CNY"}

	// Equal: zero deviation, no hint.
	d, err := Compare(Manual{MonthlyCents: 1000, Currency: "CNY"}, quote, 30)
	if err != nil || d.Pct != 0 || d.Off {
		t.Fatalf("equal: dev=%+v err=%v", d, err)
	}
	// Manual 38% pricier: beyond the 30 tolerance, hint given, Pct positive.
	d, err = Compare(Manual{MonthlyCents: 1380, Currency: "CNY"}, quote, 30)
	if err != nil || !d.Off || d.Pct != 38 {
		t.Fatalf("pricier: dev=%+v err=%v", d, err)
	}
	// Manual 20% cheaper: inside tolerance, no hint, Pct negative.
	d, err = Compare(Manual{MonthlyCents: 800, Currency: "CNY"}, quote, 30)
	if err != nil || d.Off || d.Pct != -20 {
		t.Fatalf("cheaper: dev=%+v err=%v", d, err)
	}
	// 50% cheaper: hinted as well (far too cheap also warrants a review, the plan tier may not line up).
	d, err = Compare(Manual{MonthlyCents: 500, Currency: "CNY"}, quote, 30)
	if err != nil || !d.Off || d.Pct != -50 {
		t.Fatalf("too cheap: dev=%+v err=%v", d, err)
	}
	// Boundary: exactly at the tolerance, no hint (Off only above 30).
	d, err = Compare(Manual{MonthlyCents: 1300, Currency: "CNY"}, quote, 30)
	if err != nil || d.Off || d.Pct != 30 {
		t.Fatalf("boundary: dev=%+v err=%v", d, err)
	}

	// Currency mismatch refuses to compare.
	if _, err := Compare(Manual{MonthlyCents: 1000, Currency: "USD"}, quote, 30); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("currency err = %v", err)
	}
	// Manual price not filled in.
	if _, err := Compare(Manual{MonthlyCents: 0, Currency: "CNY"}, quote, 30); !errors.Is(err, ErrNoManual) {
		t.Fatalf("no manual err = %v", err)
	}
	// Reference price invalid.
	if _, err := Compare(Manual{MonthlyCents: 1000, Currency: "CNY"}, Quote{MonthlyCents: -1, Currency: "CNY"}, 30); !errors.Is(err, ErrNoQuote) {
		t.Fatalf("no quote err = %v", err)
	}
}
