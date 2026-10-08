// Package costref 是成本参考库插件位（《套餐与成本设计》§4）：参考牌价
// 来源的统一契约。口径三条——
//
//  1. 手录成本唯一权威：nodes 上的单价/月固定成本是分配与看板的数据源，
//     参考价只做提示，永不自动改手录价；
//  2. 来源横扩 = 实现一个 Source：infracost 类开源价格库面向 AWS/GCP/Azure
//     的 terraform 资源、没有 VPS/中转商家口径，故首形态不接库，走数据
//     驱动的公开价格表（StaticSource，面板导入维护）；后续接库前先核
//     license（开源优先令）；
//  3. Compare 产偏差提示（手录价偏离牌价），不产动作——人工复核。
//
// 本包只依赖标准库，server 与未来工具侧共用。
package costref

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound 是来源里查不到对应牌价（调用方按未命中处理，不算错账）。
var ErrNotFound = errors.New("costref: no reference price for query")

// Source 是一家参考价来源的插件位（设计 §4.1）。
type Source interface {
	// Name 是来源名（infracost / pricelist / manual_watch …），随 Quote
	// 与日志落痕。
	Name() string
	// Lookup 按机型/区域/配置查参考牌价（月付价与流量单价）。
	// 查不到回 ErrNotFound。
	Lookup(ctx context.Context, q Query) (Quote, error)
}

// Query 是一次参考价查询的定位键。
type Query struct {
	Provider string `json:"provider"` // 云厂商/商家
	Region   string `json:"region"`
	Spec     string `json:"spec"` // 机型配置（CPU/内存/带宽/流量），来源内自管词表
}

// Quote 是一条参考牌价。
type Quote struct {
	MonthlyCents int64     `json:"monthly_cents"` // 月付牌价（分/月）
	TrafficPrice int64     `json:"traffic_price"` // 流量单价（分/GB），无则 0
	Currency     string    `json:"currency"`      // 牌价币种
	URL          string    `json:"url,omitempty"` // 价格页，可点开核对
	At           time.Time `json:"at"`            // 快照时刻
}
