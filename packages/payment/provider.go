// Package payment 定义收款 Provider 契约与注册表：server 与 payments/ 插件共用，
// 卡密（card）零资质先行，epusdt/微信/支付宝按阶段接入（见《支付设计》§1）。
package payment

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Provider 是一种收款渠道的契约。
type Provider interface {
	Name() string
	// CreateOrder 创建收款订单，返回外部收款标识与跳转地址（卡密类为空）。
	CreateOrder(ctx context.Context, o Order) (Receipt, error)
	// Verify 验证异步回调，返回可信的订单号与外部交易号。
	Verify(ctx context.Context, raw []byte) (Callback, error)
}

// Order 是面板发起的收款订单。
type Order struct {
	OrderNo     string // 面板生成的订单号
	UserID      int64  // 目标用户
	AmountCents int64  // 金额（分），卡密兑换为 0
	Product     string // 商品描述，如「50GB 流量卡密」
	CreatedAt   time.Time
}

// Receipt 是 Provider 对下单的应答。
type Receipt struct {
	Provider   string // card / epusdt / wechat / alipay
	PayURL     string // 收款页地址，卡密为空
	ExternalID string // 外部订单号（epusdt 等），卡密为空
	ExpiresAt  time.Time
}

// Callback 是验签后的回调结论。
type Callback struct {
	OrderNo     string
	ExternalID  string // 交易号/txid
	AmountCents int64
	PaidAt      time.Time
	Raw         []byte // 原始回调，落流水表备查
}

var (
	regMu    sync.RWMutex
	registry = map[string]Provider{}
)

// Register 注册一个 Provider；同名后注册者覆盖前者（payments/ 插件以 init() 自注册）。
func Register(p Provider) {
	regMu.Lock()
	defer regMu.Unlock()
	registry[p.Name()] = p
}

// Get 按名称取 Provider，未注册报错（server 只对启用的渠道放行调用）。
func Get(name string) (Provider, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	p, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("payment provider %q not registered", name)
	}
	return p, nil
}

// Names 返回已注册渠道名，字典序（诊断与配置校验用）。
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
