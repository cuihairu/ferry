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

// TestChannels 覆盖渠道注册态枚举（SHOP-1）：真实现记 enabled，
// 占位渠道记 disabled，且按名称字典序返回。
func TestChannels(t *testing.T) {
	Register(NewDisabled("wx", "等商户资质"))
	Register(stubProvider{name: "real"})
	states := Channels()
	byName := map[string]ChannelState{}
	for _, s := range states {
		byName[s.Name] = s
	}
	if _, ok := byName["wx"]; ok && byName["wx"].Enabled {
		t.Fatalf("placeholder wx should be disabled: %+v", states)
	}
	if _, ok := byName["real"]; ok && !byName["real"].Enabled {
		t.Fatalf("real provider should be enabled: %+v", states)
	}
	// 字典序
	for i := 1; i < len(states); i++ {
		if states[i-1].Name > states[i].Name {
			t.Fatalf("channels not sorted: %+v", states)
		}
	}
}
