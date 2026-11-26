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
