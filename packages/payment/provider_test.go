package payment

import (
	"context"
	"errors"
	"testing"
)

// stubProvider 是满足契约的最小实现，编译期校验接口形状。
type stubProvider struct{ name string }

func (s stubProvider) Name() string { return s.name }
func (s stubProvider) CreateOrder(context.Context, Order) (Receipt, error) {
	return Receipt{}, errors.New("未启用")
}
func (s stubProvider) Verify(context.Context, []byte) (Callback, error) {
	return Callback{}, errors.New("未启用")
}

var _ Provider = stubProvider{}

func TestRegistry(t *testing.T) {
	Register(stubProvider{name: "card"})
	Register(stubProvider{name: "epusdt"})
	// 同名覆盖不报错
	Register(stubProvider{name: "card"})

	p, err := Get("card")
	if err != nil || p.Name() != "card" {
		t.Fatalf("get card: %v %v", p, err)
	}
	if _, err := Get("wechat"); err == nil {
		t.Fatal("unregistered provider should error")
	}
	names := Names()
	if len(names) != 2 || names[0] != "card" || names[1] != "epusdt" {
		t.Fatalf("names = %v", names)
	}
}
