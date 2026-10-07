// Package alipay 是支付宝渠道占位（PAY-10）：注册占位 Provider 使
// 渠道枚举完整、下单得到「未启用」文案；商户资质到位后在此替换为
// 真实现（当面付/手机网站下单 + RSA2 回调验签）。
package alipay

import "github.com/cuihairu/ferry/packages/payment"

func init() { payment.Register(payment.NewDisabled("alipay", "等商户资质")) }
