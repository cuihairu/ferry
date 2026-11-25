package host

import "testing"

// TestSample asserts the sampling semantics against a real host: fields within legal ranges and cumulative bytes monotonically non-decreasing.
// the collection implementation now uses gopsutil (OSS-1), so parsing details are covered by the library's own tests.
func TestSample(t *testing.T) {
	s := Sample()
	if s.MemTotalBytes > 0 && s.MemUsedBytes > s.MemTotalBytes {
		t.Fatalf("used %d exceeds total %d", s.MemUsedBytes, s.MemTotalBytes)
	}
	if s.Load1 < 0 {
		t.Fatalf("load1 = %v", s.Load1)
	}
	if s.CPUUtil < 0 || s.CPUUtil > 100 {
		t.Fatalf("cpu util = %v", s.CPUUtil)
	}
	if s.TCPConns < 0 {
		t.Fatalf("conns = %d", s.TCPConns)
	}
	// cumulative bytes are monotonically non-decreasing: no item should shrink between two consecutive samples
	again := Sample()
	if again.NetRxBytes < s.NetRxBytes || again.NetTxBytes < s.NetTxBytes {
		t.Fatalf("net counters must not decrease: (%d,%d) -> (%d,%d)",
			s.NetRxBytes, s.NetTxBytes, again.NetRxBytes, again.NetTxBytes)
	}
	// the first-sample CPUUtil=0 rule: after the library switch the second sample onward should start reporting numbers (0-100).
}
