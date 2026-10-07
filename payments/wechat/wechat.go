// Package wechat 是微信支付渠道占位（PAY-10）：注册占位 Provider 使
// 渠道枚举完整、下单得到「未启用」文案；商户资质到位后在此替换为
// 真实现（V3 JSAPI/Native 下单 + 平台证书回调验签）。
package wechat

import "github.com/cuihairu/ferry/packages/payment"

func init() { payment.Register(payment.NewDisabled("wechat", "等商户资质")) }
