package quality

import (
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestGameSetMetrics pins the F-0941 set: a game is judged by exactly rtt,
// jitter, loss and availability, in that order, and an unknown business gets
// no metrics rather than a mislabeled default.
func TestGameSetMetrics(t *testing.T) {
	set := Set(Game)
	if set.Business != Game {
		t.Fatalf("business = %q", set.Business)
	}
	want := []Metric{MetricRTT, MetricJitter, MetricLoss, MetricAvail}
	if len(set.Metrics) != len(want) {
		t.Fatalf("game metrics = %d; want %d", len(set.Metrics), len(want))
	}
	for i, m := range want {
		if set.Metrics[i].Metric != m {
			t.Fatalf("game metric[%d] = %q; want %q", i, set.Metrics[i].Metric, m)
		}
		if set.Metrics[i].Derive == nil {
			t.Fatalf("game metric %q has no derivation", m)
		}
	}

	if got := Set("vending-machine"); len(got.Metrics) != 0 {
		t.Fatalf("unknown business must return an empty set, got %+v", got.Metrics)
	}
}

// TestGameSetDerive anchors each game metric to the ProbeReport field it
// reads, including the two degenerate cases: unreachable is 0 availability,
// and reachable with everything lost is still 0.
func TestGameSetDerive(t *testing.T) {
	set := Set(Game)
	r := storage.ProbeReport{RttMs: 42, JitterMs: 7, LossPct: 5, Reachable: true}
	want := map[Metric]int{MetricRTT: 42, MetricJitter: 7, MetricLoss: 5, MetricAvail: 95}
	for _, mi := range set.Metrics {
		if got := mi.Derive(r); got != want[mi.Metric] {
			t.Fatalf("%s = %d; want %d", mi.Metric, got, want[mi.Metric])
		}
	}

	dead := storage.ProbeReport{LossPct: 100, Reachable: false}
	for _, mi := range set.Metrics {
		if mi.Metric == MetricAvail && mi.Derive(dead) != 0 {
			t.Fatalf("unreachable avail = %d; want 0", mi.Derive(dead))
		}
	}
	allLost := storage.ProbeReport{LossPct: 100, Reachable: true}
	for _, mi := range set.Metrics {
		if mi.Metric == MetricAvail && mi.Derive(allLost) != 0 {
			t.Fatalf("all-lost avail = %d; want 0", mi.Derive(allLost))
		}
	}
}

// TestWebSetMetrics pins the F-0942 set: connect, TLS, TTFB and the round
// failure rate, each straight off the report's phase fields.
func TestWebSetMetrics(t *testing.T) {
	set := Set(Web)
	if set.Business != Web || len(set.Metrics) != 4 {
		t.Fatalf("web set = %+v; want 4 metrics", set)
	}
	want := []Metric{MetricConnect, MetricTLS, MetricTTFB, MetricLoss}
	for i, m := range want {
		if set.Metrics[i].Metric != m {
			t.Fatalf("web metric[%d] = %q; want %q", i, set.Metrics[i].Metric, m)
		}
	}
	r := storage.ProbeReport{ConnectMs: 12, TLSMs: 40, TTFBMs: 90, LossPct: 10}
	if got := set.Metrics[0].Derive(r); got != 12 {
		t.Fatalf("web connect = %d", got)
	}
	if got := set.Metrics[1].Derive(r); got != 40 {
		t.Fatalf("web tls = %d", got)
	}
	if got := set.Metrics[2].Derive(r); got != 90 {
		t.Fatalf("web ttfb = %d", got)
	}
	if got := set.Metrics[3].Derive(r); got != 10 {
		t.Fatalf("web loss = %d", got)
	}
	// A plaintext probe carries tls 0: absence of TLS is a datum, not absence
	// of measurement, so the set reads the field without interpretation.
	if got := set.Metrics[1].Derive(storage.ProbeReport{}); got != 0 {
		t.Fatalf("plaintext tls = %d", got)
	}
}

// TestVideoSetMetrics pins the F-0943 set: throughput, loss, jitter and the
// stall-risk proxy.
func TestVideoSetMetrics(t *testing.T) {
	set := Set(Video)
	if set.Business != Video || len(set.Metrics) != 4 {
		t.Fatalf("video set = %+v; want 4 metrics", set)
	}
	want := []Metric{MetricThroughput, MetricLoss, MetricJitter, MetricBufferRisk}
	for i, m := range want {
		if set.Metrics[i].Metric != m {
			t.Fatalf("video metric[%d] = %q; want %q", i, set.Metrics[i].Metric, m)
		}
	}
	r := storage.ProbeReport{ThroughputKbps: 8000, LossPct: 10, JitterMs: 15, Reachable: true}
	if got := set.Metrics[0].Derive(r); got != 8000 {
		t.Fatalf("video throughput = %d", got)
	}
	// buffer risk: loss counts double, jitter ms-for-points, capped at 100.
	if got := set.Metrics[3].Derive(r); got != 35 {
		t.Fatalf("buffer risk = %d; want 35", got)
	}
	if got := set.Metrics[3].Derive(storage.ProbeReport{LossPct: 90, JitterMs: 90, Reachable: true}); got != 100 {
		t.Fatalf("buffer risk cap = %d; want 100", got)
	}
	if got := set.Metrics[3].Derive(storage.ProbeReport{LossPct: 0, JitterMs: 0, Reachable: false}); got != 100 {
		t.Fatalf("unreachable buffer risk = %d; want 100", got)
	}
}

// TestProxySetMetrics pins the F-0944 set: latency, loss and availability --
// the transport is the experience.
func TestProxySetMetrics(t *testing.T) {
	set := Set(Proxy)
	if set.Business != Proxy || len(set.Metrics) != 3 {
		t.Fatalf("proxy set = %+v; want 3 metrics", set)
	}
	want := []Metric{MetricRTT, MetricLoss, MetricAvail}
	for i, m := range want {
		if set.Metrics[i].Metric != m {
			t.Fatalf("proxy metric[%d] = %q; want %q", i, set.Metrics[i].Metric, m)
		}
	}
	r := storage.ProbeReport{RttMs: 120, LossPct: 20, Reachable: true}
	if got := set.Metrics[2].Derive(r); got != 80 {
		t.Fatalf("proxy avail = %d; want 80", got)
	}
}
