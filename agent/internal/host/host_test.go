package host

import "testing"

func TestCutKV(t *testing.T) {
	cases := []struct {
		line string
		key  string
		val  uint64
		ok   bool
	}{
		{"MemTotal:       16308180 kB", "MemTotal", 16308180, true},
		{"MemAvailable:    999 kB", "MemAvailable", 999, true},
		{"MemFree:          100 kB", "MemFree", 100, true},
		{"no colon here", "", 0, false},
		{"Key: notanumber kB", "", 0, false},
		{"Key:", "", 0, false},
	}
	for _, tc := range cases {
		k, v, ok := cutKV(tc.line)
		if ok != tc.ok || k != tc.key || v != tc.val {
			t.Fatalf("cutKV(%q) = (%q,%d,%v), want (%q,%d,%v)", tc.line, k, v, ok, tc.key, tc.val, tc.ok)
		}
	}
}

func TestSample(t *testing.T) {
	s := Sample()
	if s.MemTotalBytes > 0 && s.MemUsedBytes > s.MemTotalBytes {
		t.Fatalf("used %d exceeds total %d", s.MemUsedBytes, s.MemTotalBytes)
	}
	if s.Load1 < 0 {
		t.Fatalf("load1 = %v", s.Load1)
	}
}
