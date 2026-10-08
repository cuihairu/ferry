package costref

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Entry 是公开价格表里的一行：一个商家/区域/配置的牌价快照。
// 面板以 JSON 数组导入维护（见 ParseTable），来源名随 NewStatic 固定。
type Entry struct {
	Provider     string `json:"provider"`                // 云厂商/商家
	Region       string `json:"region"`                  // 区域
	Spec         string `json:"spec"`                    // 机型配置（来源内自管词表）
	MonthlyCents int64  `json:"monthly_cents"`           // 月付牌价（分/月），必填
	TrafficPrice int64  `json:"traffic_price,omitempty"` // 流量单价（分/GB），可空
	Currency     string `json:"currency"`                // 币种，缺省 CNY
	URL          string `json:"url,omitempty"`           // 价格页，可点开核对
}

// StaticSource 是数据驱动的公开价格表来源：横扩真实在线来源前，
// 先用导入的价格表把偏差提示链路跑通（设计 §4.2「公开价格表」形态）。
// At 是整表快照时刻，Lookup 回填 Quote.At（牌价是快照不是实时）。
type StaticSource struct {
	name    string
	at      time.Time
	entries map[string]Entry // 归一化(provider|region|spec) → 行
}

// NewStatic 以一组价格表行造来源；重复键后行覆盖前行。
func NewStatic(name string, entries []Entry) *StaticSource {
	s := &StaticSource{name: name, entries: make(map[string]Entry, len(entries))}
	for _, e := range entries {
		s.entries[key(e.Provider, e.Region, e.Spec)] = e
	}
	return s
}

// Name 实现来源名（缺省 pricelist）。
func (s *StaticSource) Name() string {
	if s.name == "" {
		return "pricelist"
	}
	return s.name
}

// Lookup 精确匹配 provider+region+spec（大小写不敏感、去首尾空白）。
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

// ParseTable 把 JSON 价格表（[]Entry）解析成来源，快照时刻取当前时间；
// 导入层校验：非法 JSON、缺 provider/spec、月价非正都拒——脏表进偏差
// 提示就是脏提示。
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
