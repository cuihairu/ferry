package costref

import (
	"context"
	"testing"
)

// TestStaticSourceDefaultName 未命名来源回落 pricelist 缺省名。
func TestStaticSourceDefaultName(t *testing.T) {
	s := NewStatic("", []Entry{{Provider: "p", Region: "r", Spec: "s", MonthlyCents: 100}})
	if got := s.Name(); got != "pricelist" {
		t.Fatalf("Name = %q; want pricelist", got)
	}
	if _, err := s.Lookup(context.Background(), Query{Provider: "p", Region: "r", Spec: "s"}); err != nil {
		t.Fatalf("lookup on default-named source: %v", err)
	}
}
