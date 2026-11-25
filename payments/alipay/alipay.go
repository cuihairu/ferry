// Package alipay is the Alipay channel placeholder (PAY-10): it registers a placeholder Provider so
// the channel listing stays complete and ordering yields a "not enabled" message; once merchant
// credentials are in place, replace it here with the real implementation (face-to-face / mobile-web
// ordering plus RSA2 callback signature verification).
package alipay

import "github.com/cuihairu/ferry/packages/payment"

func init() { payment.Register(payment.NewDisabled("alipay", "等商户资质")) }
