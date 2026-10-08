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

// 成本参考库（E-31）接线：手录成本唯一权威（alloc/看板口径不变），
// 参考价只产偏差提示永不改价；首形态=公开价格表（JSON 落 settings，
// 面板导入维护），横扩在线来源=costref.Source 另实现（接库前核 license）。

// SettingRefTable 是参考价表 settings 键（JSON []costref.Entry，空=未启用）。
const SettingRefTable = "costref_static_table"

// DefaultRefTolerancePct 缺省偏差容忍度：|手录-牌价|>30% 提示人工复核。
const DefaultRefTolerancePct = 30

// LoadRefSource 读价格表建来源；未导入回 (nil, nil)。
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

// SaveRefTable 校验后落库（导入层拒脏表）；空串清空停用。
func SaveRefTable(db *gorm.DB, data []byte) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		return storage.SetSetting(db, SettingRefTable, "")
	}
	if _, err := costref.ParseTable(data); err != nil {
		return err
	}
	return storage.SetSetting(db, SettingRefTable, string(data))
}

// NodeQuery 把节点映射成牌价键：商家=机房（必填）、区域、配置=套餐档
// 「下行带宽-月流量」。映射是提示用启发式，节点侧字段以面板为准。
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

// SpecOf 拼套餐档位串「<下行>M-<月流量>G」，0 段省略；流量按十进制 GB
// 与流量单价同口径（BytesPerGB）。
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

// CheckNode 单节点对账：手录月固定成本 vs 牌价（只比月固定，流量单价
// 偏差提示随价格扫描批再接）。查不到牌价/币种不符/手录未填都回错误，
// 由调用方分面提示。
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
