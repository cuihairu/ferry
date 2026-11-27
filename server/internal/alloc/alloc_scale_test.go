package alloc

import (
	"fmt"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// scaleCandidates builds n candidates with deterministic load/cost/health
// spread: every 5th candidate is sick, load ratios and metered prices sweep
// their ranges, names sort lexicographically so the deterministic tiebreak
// always has an anchor.
func scaleCandidates(n int) []Candidate {
	cands := make([]Candidate, 0, n)
	for i := 0; i < n; i++ {
		cands = append(cands, Candidate{
			Node: storage.Node{
				Name:              fmt.Sprintf("scale-%05d", i),
				BillingType:       "按流量",
				TrafficPriceCents: int64(i % 97),
				BwDownMbps:        10 + i%990,
			},
			Conns:     i % 4000,
			Healthy:   i%5 != 0,
			CostKnown: true,
			CostFresh: true,
		})
	}
	return cands
}

// TestPickScaleDeterministicAndCorrect drives Pick with a large candidate
// pool (F-2066): the scale run must keep the policy semantics exact --
// health gate still filters the sick fifth, the winner is the expected
// least-loaded healthy candidate, and repeated runs are deterministic.
func TestPickScaleDeterministicAndCorrect(t *testing.T) {
	const n = 5000
	cands := scaleCandidates(n)
	w := DefaultPolicySetting().Weights()

	for _, policy := range []string{PolicyLeastConn, PolicyCostFirst, PolicyPerfFirst, PolicyBalanced, PolicyPriority} {
		idx, ok := Pick(policy, "out", MissingCostDeprioritize, w, cands)
		if !ok {
			t.Fatalf("%s: no winner in %d candidates", policy, n)
		}
		if !cands[idx].Healthy {
			t.Fatalf("%s: winner %d is sick (health gate broken at scale)", policy, idx)
		}
		// Determinism: same input, same winner, every time.
		for run := 0; run < 50; run++ {
			idx2, ok2 := Pick(policy, "out", MissingCostDeprioritize, w, cands)
			if !ok2 || idx2 != idx {
				t.Fatalf("%s: winner moved between runs (%d vs %d)", policy, idx, idx2)
			}
		}
	}

	// least_conn exact winner via an independent oracle: minimal
	// conns/capacity among healthy candidates, name lexicographic on ties.
	idx, ok := Pick(PolicyLeastConn, "out", MissingCostDeprioritize, w, cands)
	if !ok {
		t.Fatal("least_conn: no winner")
	}
	want := -1
	wantRatio := 0.0
	for i, c := range cands {
		if !c.Healthy {
			continue
		}
		r := float64(c.Conns) / float64(c.CapacityMbps())
		if want < 0 || r < wantRatio || (r == wantRatio && c.Node.Name < cands[want].Node.Name) {
			want, wantRatio = i, r
		}
	}
	if idx != want {
		t.Fatalf("least_conn scale winner wrong: got %s want %s", cands[idx].Node.Name, cands[want].Node.Name)
	}

	// Health gate at scale: everything sick => no allocation, fast.
	allSick := make([]Candidate, n)
	for i := range allSick {
		allSick[i] = cands[i]
		allSick[i].Healthy = false
	}
	if _, ok := Pick(PolicyLeastConn, "out", MissingCostDeprioritize, w, allSick); ok {
		t.Fatal("all-sick pool must not allocate")
	}
}

// BenchmarkPickLargePool measures the per-alloc cost of candidate selection at
// 5000 candidates (F-2066): the scheduler picks per connection, so this must
// stay far below any per-connection budget. Numbers land in
// docs/testing/scale-report.md.
func BenchmarkPickLargePool(b *testing.B) {
	cands := scaleCandidates(5000)
	w := DefaultPolicySetting().Weights()
	policies := []string{PolicyLeastConn, PolicyCostFirst, PolicyPerfFirst, PolicyBalanced}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := Pick(policies[i%len(policies)], "out", MissingCostDeprioritize, w, cands); !ok {
			b.Fatal("no winner")
		}
	}
}
