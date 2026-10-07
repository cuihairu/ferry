// Package host 读取本机负载与内存快照（OSS-1：经 shirou/gopsutil/v4
// 替换原 /proc 手解析，MIT），读取失败对应字段返回零值不报错。
package host

import (
	"sync"

	gcpu "github.com/shirou/gopsutil/v4/cpu"
	gsys "github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

// Snapshot 是一次本机状态采样。
type Snapshot struct {
	UptimeSec     int64
	Load1         float64
	CPUUtil       float64 // CPU 使用率 0-100（首次采样为 0）
	MemUsedBytes  uint64
	MemTotalBytes uint64
	NetRxBytes    uint64 // 开机以来累计接收字节（不含 lo）
	NetTxBytes    uint64 // 开机以来累计发送字节（不含 lo）
	TCPConns      int    // 本机 ESTABLISHED 连接数
}

// Sample 采集当前快照。
func Sample() Snapshot {
	s := Snapshot{}
	if up, err := gsys.Uptime(); err == nil {
		s.UptimeSec = int64(up)
	}
	if avg, err := load.Avg(); err == nil {
		s.Load1 = avg.Load1
	}
	s.CPUUtil = cpuUtilPercent()
	if vm, err := mem.VirtualMemory(); err == nil {
		s.MemTotalBytes = vm.Total
		s.MemUsedBytes = vm.Used
	}
	if counters, err := gnet.IOCounters(true); err == nil {
		for _, c := range counters {
			if c.Name == "lo" {
				continue
			}
			s.NetRxBytes += c.BytesRecv
			s.NetTxBytes += c.BytesSent
		}
	}
	s.TCPConns = establishedCount()
	return s
}

var cpuFirst struct {
	mu   sync.Mutex
	seen bool
}

// cpuUtilPercent 返回自上次采样以来的 CPU 使用率；首次采样只建基线返回 0
//（cpu.Percent(0) 即差分口径，这里补齐首采样为 0 的原语义）。
func cpuUtilPercent() float64 {
	cpuFirst.mu.Lock()
	defer cpuFirst.mu.Unlock()
	if !cpuFirst.seen {
		cpuFirst.seen = true
		return 0
	}
	ps, err := gcpu.Percent(0, false)
	if err != nil || len(ps) == 0 {
		return 0
	}
	return ps[0]
}

// establishedCount 统计本机 ESTABLISHED 的 TCP 连接数（v4+v6）。
func establishedCount() int {
	n := 0
	for _, kind := range []string{"tcp4", "tcp6"} {
		conns, err := gnet.Connections(kind)
		if err != nil {
			continue
		}
		for _, c := range conns {
			if c.Status == "ESTABLISHED" {
				n++
			}
		}
	}
	return n
}
