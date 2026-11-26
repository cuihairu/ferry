package storage

import "testing"

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
