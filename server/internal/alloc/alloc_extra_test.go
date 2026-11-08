package alloc

import (
	"testing"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPickTieBreaks 覆盖 lessFor 各策略的平手回退（名称字典序/优质线/成本）。
func TestPickTieBreaks(t *testing.T) {
	w := DefaultPolicySetting().Weights()
	// least_conn：负载与成本全同 → 名称字典序。
	cands := []Candidate{
		{Node: storage.Node{Name: "b", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 5, Healthy: true},
		{Node: storage.Node{Name: "a", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 5, Healthy: true},
	}
	idx, _ := Pick(PolicyLeastConn, "out", w, cands)
	if cands[idx].Node.Name != "a" {
		t.Fatalf("least_conn tie name: %s", cands[idx].Node.Name)
	}
	// cost_first：成本同 → 负载低者胜。
	cands = []Candidate{
		{Node: storage.Node{Name: "busy", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 50, Healthy: true},
		{Node: storage.Node{Name: "idle", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 5, Healthy: true},
	}
	idx, _ = Pick(PolicyCostFirst, "out", w, cands)
	if cands[idx].Node.Name != "idle" {
		t.Fatalf("cost_first load tie: %s", cands[idx].Node.Name)
	}
	// perf_first：优质线（in 向）优先于非优质，即使负载更高。
	cands = []Candidate{
		{Node: storage.Node{Name: "premium", BwDownMbps: 10, LineType: "cn2_gia"}, Conns: 100, Healthy: true},
		{Node: storage.Node{Name: "plain", BwDownMbps: 10}, Conns: 1, Healthy: true},
	}
	idx, _ = Pick(PolicyPerfFirst, agentproto.DirectionIn, w, cands)
	if cands[idx].Node.Name != "premium" {
		t.Fatalf("perf_first premium: %s", cands[idx].Node.Name)
	}
	// perf_first 非优质同负载 → 成本低者胜。
	cands = []Candidate{
		{Node: storage.Node{Name: "cheap", BwDownMbps: 10, TrafficPriceCents: 1, BillingType: "按流量"}, Conns: 5, Healthy: true},
		{Node: storage.Node{Name: "pricey", BwDownMbps: 10, TrafficPriceCents: 9, BillingType: "按流量"}, Conns: 5, Healthy: true},
	}
	idx, _ = Pick(PolicyPerfFirst, "out", w, cands)
	if cands[idx].Node.Name != "cheap" {
		t.Fatalf("perf_first cost: %s", cands[idx].Node.Name)
	}
	// balanced：负载成本全同 → 名称字典序。
	cands = []Candidate{
		{Node: storage.Node{Name: "z", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 5, Healthy: true},
		{Node: storage.Node{Name: "y", BwDownMbps: 10, TrafficPriceCents: 5, BillingType: "按流量"}, Conns: 5, Healthy: true},
	}
	idx, _ = Pick(PolicyBalanced, "out", w, cands)
	if cands[idx].Node.Name != "y" {
		t.Fatalf("balanced tie name: %s", cands[idx].Node.Name)
	}
	// balanced 优质线减项（in 向）：优质线胜。
	cands = []Candidate{
		{Node: storage.Node{Name: "prem", BwDownMbps: 10, LineType: "iplc"}, Conns: 5, Healthy: true},
		{Node: storage.Node{Name: "normal", BwDownMbps: 10}, Conns: 10, Healthy: true},
	}
	idx, _ = Pick(PolicyBalanced, agentproto.DirectionIn, w, cands)
	if cands[idx].Node.Name != "prem" {
		t.Fatalf("balanced premium: %s", cands[idx].Node.Name)
	}
}
