package host

import "testing"

// TestSample 对真实主机采样做口径断言：字段范围合法、累计字节单调不减。
// 采集实现已换 gopsutil（OSS-1），解析细节由库自身测试覆盖。
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
	// 首采样 CPUUtil=0 的口径：换包后第二次采样起应开始出数（0-100）。
}
