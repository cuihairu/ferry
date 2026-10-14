package xray

import (
	"context"
	"testing"
)

// NoopHandler 必须满足 Handler 接口（编译期断言）。
var _ Handler = NoopHandler{}

func TestNoopHandler(t *testing.T) {
	h := NoopHandler{}
	ctx := context.Background()

	rx, tx, err := h.QueryUserTraffic(ctx, "u1@example.com")
	if err != nil || rx != 0 || tx != 0 {
		t.Fatalf("QueryUserTraffic = %d, %d, %v; want 0, 0, nil", rx, tx, err)
	}
	if err := h.AddInbound(ctx, []byte(`{"tag":"in1"}`)); err != nil {
		t.Fatalf("AddInbound = %v; want nil", err)
	}
	if err := h.RemoveInbound(ctx, "in1"); err != nil {
		t.Fatalf("RemoveInbound = %v; want nil", err)
	}
}
