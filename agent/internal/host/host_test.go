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
	if s.CPUUtil < 0 || s.CPUUtil > 100 {
		t.Fatalf("cpu util = %v", s.CPUUtil)
	}
	if s.TCPConns < 0 {
		t.Fatalf("conns = %d", s.TCPConns)
	}
	// 累计字节单调不减：连续两次采样后一项不应更小
	again := Sample()
	if again.NetRxBytes < s.NetRxBytes || again.NetTxBytes < s.NetTxBytes {
		t.Fatalf("net counters must not decrease: (%d,%d) -> (%d,%d)",
			s.NetRxBytes, s.NetTxBytes, again.NetRxBytes, again.NetTxBytes)
	}
}

func TestCpuStat(t *testing.T) {
	// cpu user nice system idle iowait irq softirq steal
	content := "cpu  100 20 30 400 50 2 1 0\ncpu0 50 10 15 200 25 1 0 0\n"
	total, idle, ok := cpuStat(content)
	if !ok {
		t.Fatal("cpuStat should parse")
	}
	if total != 603 {
		t.Fatalf("total = %d, want 603", total)
	}
	if idle != 450 { // idle 400 + iowait 50
		t.Fatalf("idle = %d, want 450", idle)
	}
	if _, _, ok := cpuStat("garbage\n"); ok {
		t.Fatal("garbage must not parse")
	}
}

func TestCpuUtilBetween(t *testing.T) {
	// 前段 total=1000 idle=800，后段 total=1100 idle=850：
	// 忙 50 tick / 总 100 tick = 50%
	util := cpuUtilBetween(1000, 800, 1100, 850)
	if util != 50 {
		t.Fatalf("util = %v, want 50", util)
	}
	if u := cpuUtilBetween(1000, 800, 1000, 800); u != 0 {
		t.Fatalf("no delta must be 0, got %v", u)
	}
}

func TestNetDevBytes(t *testing.T) {
	content := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  1000       10    0    0    0     0          0         0  1000       10    0    0    0     0       0          0
  eth0:  5000      100    0    0    0     0          0         0  2000       80    0    0    0     0       0          0
  eth1:  3000       50    0    0    0     0          0         0   700       40    0    0    0     0       0          0
`
	rx, tx := netDevBytes(content)
	if rx != 8000 { // eth0 5000 + eth1 3000，排除 lo
		t.Fatalf("rx = %d, want 8000", rx)
	}
	if tx != 2700 { // eth0 2000 + eth1 700
		t.Fatalf("tx = %d, want 2700", tx)
	}
}

func TestEstablishedCount(t *testing.T) {
	content := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1
   1: 0100007F:1F90 0A000001:2328 01 00000000:00000000 00:00000000 00000000     0        0 12346 1
   2: 0100007F:1F90 0A000002:2328 06 00000000:00000000 00:00000000 00000000     0        0 12347 1
   3: 0100007F:1F90 0A000003:2328 01 00000000:00000000 00:00000000 00000000     0        0 12348 1
`
	if n := establishedCount(content); n != 2 { // 状态 01 两条
		t.Fatalf("established = %d, want 2", n)
	}
	if n := establishedCount(""); n != 0 {
		t.Fatalf("empty = %d, want 0", n)
	}
}
