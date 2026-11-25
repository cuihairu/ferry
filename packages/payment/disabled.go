package payment

import (
	"context"
	"fmt"
)

// NewDisabled returns a placeholder Provider: register it while a channel is planned
// but its credentials are not in place, so Get hits and ordering/verification get a clear
// "not enabled" message instead of "not registered", and the channel listing (Names) shows
// the full planned surface. Once credentials are ready, the real implementation registers over it.
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
