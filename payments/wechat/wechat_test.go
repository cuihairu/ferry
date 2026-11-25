package wechat

import (
	"context"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/packages/payment"
)

// TestRegistered covers init self-registration: Get hits the wechat placeholder and ordering reports "not enabled".
func TestRegistered(t *testing.T) {
	p, err := payment.Get("wechat")
	if err != nil || p.Name() != "wechat" {
		t.Fatalf("get wechat: %v %v", p, err)
	}
	if _, err := p.CreateOrder(context.Background(), payment.Order{OrderNo: "o-1", AmountCents: 100}); err == nil || !strings.Contains(err.Error(), "未启用") {
		t.Fatalf("create = %v", err)
	}
}
