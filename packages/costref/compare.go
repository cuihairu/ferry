package costref

import "errors"

// 偏差提示的边界条件：调用方按需把错误文案透给面板。
var (
	// ErrNoManual 是手录月固定成本未填（0）——无从比，不算偏差。
	ErrNoManual = errors.New("costref: manual monthly cost not set")
	// ErrNoQuote 是参考牌价无效（非正）——来源数据坏，无从比。
	ErrNoQuote = errors.New("costref: reference price not usable")
	// ErrCurrencyMismatch 是手录价与牌价币种不同——跨币种比百分比无意义，
	// 调用方先归币种再比。
	ErrCurrencyMismatch = errors.New("costref: currency mismatch between manual and quote")
)

// Manual 是手录侧价格（唯一权威，参考价永不回写）。
type Manual struct {
	MonthlyCents int64  `json:"monthly_cents"` // 月固定成本（分/月）
	Currency     string `json:"currency"`      // 币种，与 Quote.Currency 比对前必须一致
}

// Deviation 是一次手录价 vs 牌价的偏差结论。Pct 为手录相对牌价的偏差
// 百分比（正=手录贵、负=手录便宜，整除向零截断）；Off 为 |Pct| 超出
// 容忍度、该提示人工复核。
type Deviation struct {
	Pct int64 `json:"pct"`
	Off bool  `json:"off"`
}

// Compare 比对手录月固定成本与参考牌价，只产提示不产动作（口径 §4.2）。
// tolerancePct 为容忍度百分比，|偏差|>容忍度即 Off（如 30 = ±30% 外提示）。
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
