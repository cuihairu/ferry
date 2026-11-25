// Package wechat is the Wechat Pay channel placeholder (PAY-10): it registers a placeholder Provider so
// the channel listing stays complete and ordering yields a "not enabled" message; once merchant
// credentials are in place, replace it here with the real implementation (V3 JSAPI/Native ordering
// plus platform-certificate callback verification).
package wechat

import "github.com/cuihairu/ferry/packages/payment"

func init() { payment.Register(payment.NewDisabled("wechat", "等商户资质")) }
