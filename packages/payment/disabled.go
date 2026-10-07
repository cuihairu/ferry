package payment

import (
	"context"
	"fmt"
)

// NewDisabled 返回占位 Provider：渠道已规划、资质未到位时注册它，
// 使 Get 命中、下单与验签得到明确的「未启用」文案而非「未注册」，
// 渠道枚举（Names）也能列出完整规划面。资质到位后由真实现覆盖注册。
func NewDisabled(name, reason string) Provider {
	return disabled{name: name, reason: reason}
}

type disabled struct{ name, reason string }

func (d disabled) Name() string { return d.name }

func (d disabled) CreateOrder(context.Context, Order) (Receipt, error) {
	return Receipt{}, fmt.Errorf("%s 未启用（%s）", d.name, d.reason)
}

func (d disabled) Verify(context.Context, []byte) (Callback, error) {
	return Callback{}, fmt.Errorf("%s 未启用（%s）", d.name, d.reason)
}
