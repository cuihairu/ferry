package costref

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Entry is one row of the public price table: a reference-price snapshot for one vendor/region/spec.
// The panel maintains it via JSON array import (see ParseTable); the source name is fixed by NewStatic.
type Entry struct {
	Provider     string `json:"provider"`                // cloud vendor / merchant
	Region       string `json:"region"`                  // region
	Spec         string `json:"spec"`                    // machine spec (vocabulary managed within the source)
	MonthlyCents int64  `json:"monthly_cents"`           // monthly reference price (cents/month), required
	TrafficPrice int64  `json:"traffic_price,omitempty"` // traffic unit price (cents/GB), optional
	Currency     string `json:"currency"`                // currency, defaults to CNY
	URL          string `json:"url,omitempty"`           // pricing page, clickable for verification
}

// StaticSource is a data-driven public price table source: before scaling out to real
// online sources, first get the deviation-hint pipeline running with an imported price
// table (design §4.2 "public price table" form).
// At is the whole-table snapshot time; Lookup backfills Quote.At (a reference price is a snapshot, not live).
type StaticSource struct {
	name    string
	at      time.Time
	entries map[string]Entry // normalized(provider|region|spec) → row
}

// NewStatic builds a source from a set of price table rows; on duplicate keys the later row wins.
func NewStatic(name string, entries []Entry) *StaticSource {
	s := &StaticSource{name: name, entries: make(map[string]Entry, len(entries))}
	for _, e := range entries {
		s.entries[key(e.Provider, e.Region, e.Spec)] = e
	}
	return s
}

// Name returns the source name (default pricelist).
func (s *StaticSource) Name() string {
	if s.name == "" {
		return "pricelist"
	}
	return s.name
}

// Lookup matches provider+region+spec exactly (case-insensitive, surrounding whitespace trimmed).
func (s *StaticSource) Lookup(_ context.Context, q Query) (Quote, error) {
	e, ok := s.entries[key(q.Provider, q.Region, q.Spec)]
	if !ok {
		return Quote{}, ErrNotFound
	}
	cur := e.Currency
	if cur == "" {
		cur = "CNY"
	}
	return Quote{
		MonthlyCents: e.MonthlyCents,
		TrafficPrice: e.TrafficPrice,
		Currency:     cur,
		URL:          e.URL,
		At:           s.at,
	}, nil
}

func key(provider, region, spec string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "|" +
		strings.ToLower(strings.TrimSpace(region)) + "|" +
		strings.ToLower(strings.TrimSpace(spec))
}

// ParseTable parses a JSON price table ([]Entry) into a source, with the snapshot
// time taken as now; import-layer validation: invalid JSON, missing provider/spec,
// and non-positive monthly prices are all rejected — a dirty table feeding deviation
// hints means dirty hints.
func ParseTable(data []byte) (*StaticSource, error) {
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("costref: price table is not valid JSON: %w", err)
	}
	for i, e := range entries {
		if strings.TrimSpace(e.Provider) == "" || strings.TrimSpace(e.Spec) == "" {
			return nil, fmt.Errorf("costref: price table row %d: provider and spec are required", i+1)
		}
		if e.MonthlyCents <= 0 {
			return nil, fmt.Errorf("costref: price table row %d: monthly_cents must be positive", i+1)
		}
	}
	s := NewStatic("pricelist", entries)
	s.at = time.Now()
	return s, nil
}
