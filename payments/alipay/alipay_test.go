package alipay

import (
	"context"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/packages/payment"
)

// TestRegistered covers init self-registration: Get hits the alipay placeholder and ordering reports "not enabled".
func TestRegistered(t *testing.T) {
	p, err := payment.Get("alipay")
	if err != nil || p.Name() != "alipay" {
		t.Fatalf("get alipay: %v %v", p, err)
	}
	if _, err := p.CreateOrder(context.Background(), payment.Order{OrderNo: "o-1", AmountCents: 100}); err == nil || !strings.Contains(err.Error(), "未启用") {
		t.Fatalf("create = %v", err)
	}
}
