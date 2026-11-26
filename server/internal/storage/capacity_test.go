package storage

import (
	"testing"
	"time"
)

// TestNodeCapacityMbps covers the shared fallback chain (F-1061/1062/1063):
// measured calibration wins, then plan downlink, then plan uplink, and the
// 100 Mbps floor when nothing is known.
func TestNodeCapacityMbps(t *testing.T) {
	cases := []struct {
		name string
		n    Node
		want int
	}{
		{"floor", Node{}, 100},
		{"uplink only", Node{BwUpMbps: 30}, 30},
		{"downlink wins over uplink", Node{BwUpMbps: 30, BwDownMbps: 100}, 100},
		{"measured wins", Node{BwUpMbps: 30, BwDownMbps: 100, SpeedMeasuredMbps: 80}, 80},
	}
	for _, tc := range cases {
		if got := tc.n.CapacityMbps(); got != tc.want {
			t.Fatalf("%s: capacity = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestUtilizationPct covers the one utilization formula (F-1064): the ratio
// folded to percent, and -1 for an inactive node instead of a misleading 0%.
func TestUtilizationPct(t *testing.T) {
	if got := UtilizationPct(55, 100, true); got != 55 {
		t.Fatalf("55/100 = %d, want 55", got)
	}
	if got := UtilizationPct(0, 100, true); got != 0 {
		t.Fatalf("zero traffic active = %d, want 0", got)
	}
	if got := UtilizationPct(99, 100, false); got != -1 {
		t.Fatalf("inactive = %d, want -1", got)
	}
}

// TestLatestLoad covers the shared sampling (F-1064): the connection count
// takes each proc's latest sample, throughput diffs the newest pair per
// proc, and a lone sample counts toward conns only.
func TestLatestLoad(t *testing.T) {
	db := openTest(t)
	now := time.Now()
	seed := func(node uint, proc string, rx, conns int64, at time.Time) {
		t.Helper()
		if err := db.Create(&NodeTrafficLog{
			NodeID: node, Proc: proc, RxBytes: rx, TxBytes: 0, Conns: int(conns), RecordedAt: at,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	n1 := Node{Name: "load-1", Token: "t1"}
	n2 := Node{Name: "load-2", Token: "t2"}
	if err := db.Create(&n1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&n2).Error; err != nil {
		t.Fatal(err)
	}
	// n1 relay: 400MB over 60s -> 53.3Mbps, latest conns 12
	seed(n1.ID, "relay", 100e6, 10, now.Add(-2*time.Minute))
	seed(n1.ID, "relay", 500e6, 12, now.Add(-time.Minute))
	// n1 xray: one lone sample -> conns only
	seed(n1.ID, "xray", 0, 3, now.Add(-time.Minute))
	// n2: stale only -> absent
	seed(n2.ID, "relay", 900e6, 99, now.Add(-LoadFresh-time.Minute))

	samples, err := LatestLoad(db, now.Add(-LoadFresh))
	if err != nil {
		t.Fatal(err)
	}
	s1 := samples[n1.ID]
	if s1.Conns != 15 || s1.Mbps < 53.0 || s1.Mbps > 53.7 {
		t.Fatalf("n1 sample = %+v, want conns 15 mbps ~53.3", s1)
	}
	if _, ok := samples[n2.ID]; ok {
		t.Fatalf("stale-only node must be absent: %+v", samples[n2.ID])
	}
}

// TestPathCapacity covers the weakest-link estimate (F-1068): the minimum
// node capacity wins, the bottleneck is named, and a direct path skips the
// relay entirely.
func TestPathCapacity(t *testing.T) {
	entry := &Node{Name: "e", BwDownMbps: 500}
	relay := &Node{Name: "r", BwDownMbps: 30}
	landing := &Node{Name: "l", BwDownMbps: 100}
	cap, bottleneck := PathCapacity(entry, relay, landing)
	if cap != 30 || bottleneck.Name != "r" {
		t.Fatalf("relay chain = %d %+v", cap, bottleneck)
	}
	cap, bottleneck = PathCapacity(entry, nil, landing)
	if cap != 100 || bottleneck.Name != "l" {
		t.Fatalf("direct chain = %d %+v", cap, bottleneck)
	}
	// a measured calibration on one node is respected by the shared chain
	landing.SpeedMeasuredMbps = 10
	cap, bottleneck = PathCapacity(entry, nil, landing)
	if cap != 10 || bottleneck.Name != "l" {
		t.Fatalf("measured bottleneck = %d %+v", cap, bottleneck)
	}
}
