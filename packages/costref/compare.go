package costref

import "errors"

// Boundary conditions for deviation hints: callers pass the error text to the panel as needed.
var (
	// ErrNoManual is the manually recorded monthly fixed cost being unset (0) — nothing to compare, so no deviation.
	ErrNoManual = errors.New("costref: manual monthly cost not set")
	// ErrNoQuote is the reference price being invalid (non-positive) — bad source data, nothing to compare.
	ErrNoQuote = errors.New("costref: reference price not usable")
	// ErrCurrencyMismatch is the manual price and the reference price being in different currencies —
	// a cross-currency percentage comparison is meaningless, so callers normalize the currency first.
	ErrCurrencyMismatch = errors.New("costref: currency mismatch between manual and quote")
)

// Manual is the manually recorded side price (the sole authority; a reference price is never written back).
type Manual struct {
	MonthlyCents int64  `json:"monthly_cents"` // monthly fixed cost (cents/month)
	Currency     string `json:"currency"`      // currency, must match Quote.Currency before comparing
}

// Deviation is one manual-price vs reference-price deviation verdict. Pct is the
// deviation percentage of the manual price relative to the reference (positive = manual
// pricier, negative = manual cheaper, integer division truncates toward zero); Off
// marks |Pct| beyond the tolerance, meaning this warrants a manual review.
type Deviation struct {
	Pct int64 `json:"pct"`
	Off bool  `json:"off"`
}

// Compare contrasts the manual monthly fixed cost with the reference price, producing only a hint and never an action (spec §4.2).
// tolerancePct is the tolerance percentage; |deviation| above it sets Off (e.g. 30 = hint beyond ±30%).
func Compare(m Manual, q Quote, tolerancePct int64) (Deviation, error) {
	if m.MonthlyCents <= 0 {
		return Deviation{}, ErrNoManual
	}
	if q.MonthlyCents <= 0 {
		return Deviation{}, ErrNoQuote
	}
	if m.Currency != q.Currency {
		return Deviation{}, ErrCurrencyMismatch
	}
	pct := (m.MonthlyCents - q.MonthlyCents) * 100 / q.MonthlyCents
	abs := pct
	if abs < 0 {
		abs = -abs
	}
	return Deviation{Pct: pct, Off: abs > tolerancePct}, nil
}
