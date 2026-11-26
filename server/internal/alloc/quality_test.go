package alloc

import (
	"math"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// approx compares quality scores with a float tolerance (1-0.7 is not
// exactly 0.3 in binary floating point).
func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestLatestQuality covers the aggregation (F-1028): the latest report per
// prober survives, survivors are averaged per target, a stale-only target is
// absent (unknown, not zero), and only tunnel/peer targets count.
func TestLatestQuality(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()

	proberA, proberB := seedNode(t, db, "q-entry-a", "entry", "out", nil), seedNode(t, db, "q-entry-b", "entry", "out", nil)
	fresh := seedNode(t, db, "q-landing-fresh", "landing", "out", nil)
	stale := seedNode(t, db, "q-landing-stale", "landing", "out", nil)

	seed := func(prober, target uint, rtt, loss int, at time.Time) {
		t.Helper()
		tid := target
		if err := db.Create(&storage.ProbeReport{
			NodeID: prober, TargetKind: agentproto.ProbeTargetTunnel, TargetNodeID: &tid,
			Verdict: agentproto.ProbeVerdictHealthy, RttMs: rtt, LossPct: loss, ProbedAt: at,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// prober A reports twice: only the newer (rtt 20/loss 0) survives
	seed(proberA.ID, fresh.ID, 90, 30, now.Add(-2*time.Minute))
	seed(proberA.ID, fresh.ID, 20, 0, now.Add(-time.Minute))
	// prober B reports once
	seed(proberB.ID, fresh.ID, 40, 20, now.Add(-90*time.Second))
	// stale only: outside the window, must not appear
	seed(proberA.ID, stale.ID, 10, 0, now.Add(-ProbeFresh-time.Minute))

	q := LatestQuality(db)
	got, ok := q[fresh.ID]
	if !ok {
		t.Fatalf("fresh target missing from %v", q)
	}
	if got.Samples != 2 || got.RttMs != 30 || got.LossPct != 10 {
		t.Fatalf("fresh quality = %+v, want mean of (20,0) and (40,20)", got)
	}
	if _, ok := q[stale.ID]; ok {
		t.Fatalf("stale-only target must be absent: %+v", q[stale.ID])
	}
}

// TestQualityScore covers the fold: unknown is the neutral 0.5, a perfect
// line is 1, loss dominates RTT, and the caps floor catastrophic values.
func TestQualityScore(t *testing.T) {
	if s := QualityScore(Quality{}); !approx(s, 0.5) {
		t.Fatalf("no-sample score = %v, want neutral 0.5", s)
	}
	if s := QualityScore(Quality{RttMs: 0, LossPct: 0, Samples: 1}); !approx(s, 1) {
		t.Fatalf("perfect score = %v", s)
	}
	// loss 100% floors the loss term: 0.7*0 + 0.3*1 = 0.3
	if s := QualityScore(Quality{RttMs: 0, LossPct: 100, Samples: 1}); !approx(s, 0.3) {
		t.Fatalf("all-loss score = %v", s)
	}
	// RTT at the cap floors the rtt term: 0.7*1 + 0.3*0 = 0.7
	if s := QualityScore(Quality{RttMs: 500, LossPct: 0, Samples: 1}); !approx(s, 0.7) {
		t.Fatalf("cap-rtt score = %v", s)
	}
	// beyond the caps nothing scores lower
	if s := QualityScore(Quality{RttMs: 2000, LossPct: 300, Samples: 1}); !approx(s, 0) {
		t.Fatalf("beyond-cap score = %v", s)
	}
	// loss dominates: equal 20% degradation costs more on the loss term
	// (weight 0.7) than on the rtt term (weight 0.3)
	lossy := QualityScore(Quality{RttMs: 0, LossPct: 20, Samples: 1})
	slow := QualityScore(Quality{RttMs: 100, LossPct: 0, Samples: 1})
	if lossy >= slow {
		t.Fatalf("loss must dominate: lossy %v >= slow %v", lossy, slow)
	}
}

// TestDecideCarriesQuality covers the wiring: the pure model renders each
// candidate's folded quality, and an unknown-quality candidate scores the
// neutral 0.5 so cold starts compare fairly.
func TestDecideCarriesQuality(t *testing.T) {
	scored := storage.Node{ID: 51, Name: "scored", Role: "landing", Direction: "out",
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	unknown := storage.Node{ID: 52, Name: "unknown", Role: "landing", Direction: "out",
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	entry := storage.Node{ID: 53, Name: "entry", Role: "entry", Direction: "out",
		Capabilities: storage.MustJSON([]string{"relay_entry"})}

	req := &Request{
		Now: testNow, Policy: DefaultPolicySetting(),
		Entries:  []storage.Node{entry},
		Landings: []storage.Node{scored, unknown},
		Conns:    map[uint]int{}, Sick: map[uint]bool{}, Manual: map[uint]bool{},
		Current: map[uint]*storage.LandingAssignment{},
		Quality: map[uint]Quality{51: {RttMs: 100, LossPct: 0, Samples: 1}},
	}
	decs := Decide(req)
	if len(decs) != 1 || len(decs[0].Candidates) != 2 {
		t.Fatalf("decisions = %+v", decs)
	}
	byID := map[uint]CandidateEval{}
	for _, ce := range decs[0].Candidates {
		byID[ce.Candidate.Node.ID] = ce
	}
	// rtt 100ms of the 500ms cap: 0.7*1 + 0.3*(1-100/500) = 0.94
	if got := byID[51].QualityScore; !approx(got, 0.94) {
		t.Fatalf("scored quality = %v", got)
	}
	if got := byID[52].QualityScore; !approx(got, 0.5) {
		t.Fatalf("unknown quality = %v, want neutral 0.5", got)
	}
}

// TestPickQualityMode covers the quality policy (F-1044): the best-measured
// line wins regardless of price and load, an unprobed candidate scores the
// neutral 0.5 and loses to any genuinely measured line, and equal scores
// break by name.
func TestPickQualityMode(t *testing.T) {
	w := DefaultPolicySetting().Weights()
	mk := func(name string, rtt, loss, samples int, price int64, conns int) Candidate {
		return Candidate{Node: storage.Node{Name: name, BillingType: "按流量",
			TrafficPriceCents: price, BwDownMbps: 100, UpdatedAt: testNow},
			Healthy: true, Conns: conns, CostKnown: true, CostFresh: true,
			Quality: Quality{RttMs: rtt, LossPct: loss, Samples: samples}}
	}

	// a measured good line beats the cheap-but-unprobed one
	cands := []Candidate{mk("cheap-dark", 0, 0, 0, 5, 0), mk("pricey-clean", 50, 0, 2, 90, 99)}
	if idx, ok := Pick(PolicyQuality, "out", MissingCostDeprioritize, w, cands); !ok || cands[idx].Node.Name != "pricey-clean" {
		t.Fatalf("measured quality must win: idx=%d ok=%v", idx, ok)
	}

	// equal scores (both 0.97) fall back to the name
	cands = []Candidate{mk("zeta", 50, 0, 1, 5, 0), mk("alpha", 50, 0, 1, 90, 99)}
	if idx, ok := Pick(PolicyQuality, "out", MissingCostDeprioritize, w, cands); !ok || cands[idx].Node.Name != "alpha" {
		t.Fatalf("tie must break by name: idx=%d", idx)
	}
}

// TestShouldSwitchQualityMode covers the quality policy's peak rule
// (F-1044): a clearly better measurement moves, a marginal one waits for the
// off-peak rebalance, and a worse one never moves at peak.
func TestShouldSwitchQualityMode(t *testing.T) {
	w := DefaultPolicySetting().Weights()
	mk := func(name string, rtt, loss, samples int) Candidate {
		return Candidate{Node: storage.Node{Name: name, BwDownMbps: 100}, Healthy: true,
			Quality: Quality{RttMs: rtt, LossPct: loss, Samples: samples}}
	}

	// 0.97 vs neutral 0.5: clearly better
	cands := []Candidate{mk("cur", 0, 0, 0), mk("better", 50, 0, 2)}
	if !shouldSwitch(PolicyQuality, "out", MissingCostDeprioritize, w, cands, 0, 1) {
		t.Fatalf("clear quality gain must move at peak")
	}
	// 0.615 vs 0.5: inside the 0.15 margin
	cands = []Candidate{mk("cur", 0, 0, 0), mk("edge", 0, 55, 1)}
	if shouldSwitch(PolicyQuality, "out", MissingCostDeprioritize, w, cands, 0, 1) {
		t.Fatalf("marginal gain must wait for off-peak")
	}
	// 0.2 vs 0.97: a downgrade never moves at peak
	cands = []Candidate{mk("cur", 50, 0, 2), mk("worse", 400, 80, 1)}
	if shouldSwitch(PolicyQuality, "out", MissingCostDeprioritize, w, cands, 0, 1) {
		t.Fatalf("worse quality must not move at peak")
	}
}

// TestSweepQualityMode covers the quality policy at sweep level (F-1044):
// with out=quality the sweep serves the freshly measured line even when it
// is the priciest, and a settled round does not churn.
func TestSweepQualityMode(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "qm-entry", "entry", "out", nil)
	seedNode(t, db, "qm-dark", "landing", "out", func(n *storage.Node) {
		n.BillingType, n.TrafficPriceCents = "按流量", 5
	})
	clean := seedNode(t, db, "qm-clean", "landing", "out", func(n *storage.Node) {
		n.BillingType, n.TrafficPriceCents = "按流量", 90
	})
	// a fresh strong probe for the pricey landing only
	tid := clean.ID
	if err := db.Create(&storage.ProbeReport{
		NodeID: 777, TargetKind: agentproto.ProbeTargetTunnel, TargetNodeID: &tid,
		Verdict: agentproto.ProbeVerdictHealthy, RttMs: 40, LossPct: 0, ProbedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	setting := DefaultPolicySetting()
	setting.Out = PolicyQuality
	if err := SavePolicy(db, setting); err != nil {
		t.Fatal(err)
	}
	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != clean.Name {
		t.Fatalf("quality mode must serve the measured line: %+v", events)
	}
	// the settled round is quiet
	if events, err = Sweep(db); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("quality mode churned: %+v", events)
	}
}
