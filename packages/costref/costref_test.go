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

	// 命中：大小写与首尾空白归一。
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

	// 缺省币种回填后，未带币种的行原样透传 USD。
	q, err = s.Lookup(ctx, Query{Provider: "VULTR", Region: "Tokyo", Spec: "2c1g"})
	if err != nil || q.Currency != "USD" || q.MonthlyCents != 1200 {
		t.Fatalf("second row: quote=%+v err=%v", q, err)
	}

	// 未命中。
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

	// 相等：零偏差不提示。
	d, err := Compare(Manual{MonthlyCents: 1000, Currency: "CNY"}, quote, 30)
	if err != nil || d.Pct != 0 || d.Off {
		t.Fatalf("equal: dev=%+v err=%v", d, err)
	}
	// 手录贵 38%：超 30 容差提示，Pct 为正。
	d, err = Compare(Manual{MonthlyCents: 1380, Currency: "CNY"}, quote, 30)
	if err != nil || !d.Off || d.Pct != 38 {
		t.Fatalf("pricier: dev=%+v err=%v", d, err)
	}
	// 手录便宜 20%：容差内不提示，Pct 为负。
	d, err = Compare(Manual{MonthlyCents: 800, Currency: "CNY"}, quote, 30)
	if err != nil || d.Off || d.Pct != -20 {
		t.Fatalf("cheaper: dev=%+v err=%v", d, err)
	}
	// 便宜 50%：同样提示（便宜太多也复核，可能是套餐档对不上）。
	d, err = Compare(Manual{MonthlyCents: 500, Currency: "CNY"}, quote, 30)
	if err != nil || !d.Off || d.Pct != -50 {
		t.Fatalf("too cheap: dev=%+v err=%v", d, err)
	}
	// 边界：恰在容差上不提示（>30 才 Off）。
	d, err = Compare(Manual{MonthlyCents: 1300, Currency: "CNY"}, quote, 30)
	if err != nil || d.Off || d.Pct != 30 {
		t.Fatalf("boundary: dev=%+v err=%v", d, err)
	}

	// 货币不符拒比。
	if _, err := Compare(Manual{MonthlyCents: 1000, Currency: "USD"}, quote, 30); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("currency err = %v", err)
	}
	// 手录未填。
	if _, err := Compare(Manual{MonthlyCents: 0, Currency: "CNY"}, quote, 30); !errors.Is(err, ErrNoManual) {
		t.Fatalf("no manual err = %v", err)
	}
	// 牌价无效。
	if _, err := Compare(Manual{MonthlyCents: 1000, Currency: "CNY"}, Quote{MonthlyCents: -1, Currency: "CNY"}, 30); !errors.Is(err, ErrNoQuote) {
		t.Fatalf("no quote err = %v", err)
	}
}
