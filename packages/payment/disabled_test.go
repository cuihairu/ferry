package payment

import (
	"context"
	"strings"
	"testing"
)

// TestDisabled covers the placeholder Provider: name reachable, order/verify both error with "not enabled" plus a reason.
func TestDisabled(t *testing.T) {
	p := NewDisabled("wechat", "等商户资质")
	if p.Name() != "wechat" {
		t.Fatalf("name = %q", p.Name())
	}
	if _, err := p.CreateOrder(context.Background(), Order{OrderNo: "o-1", AmountCents: 100}); err == nil || !strings.Contains(err.Error(), "wechat 未启用") || !strings.Contains(err.Error(), "等商户资质") {
		t.Fatalf("create = %v", err)
	}
	if _, err := p.Verify(context.Background(), []byte("k=v")); err == nil || !strings.Contains(err.Error(), "wechat 未启用") {
		t.Fatalf("verify = %v", err)
	}
}
