// Package host reads a local load and memory snapshot (OSS-1: via shirou/gopsutil/v4
// replacing the former hand-rolled /proc parsing, MIT); a failed read yields zero for that field rather than an error.
package host

import (
	"bufio"
	"io"
	"os"
	"strings"
	"sync"

	gcpu "github.com/shirou/gopsutil/v4/cpu"
	gsys "github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

// Snapshot is one local state sample.
type Snapshot struct {
	UptimeSec     int64
	Load1         float64
	CPUUtil       float64 // CPU usage 0-100 (0 for the first sample)
	MemUsedBytes  uint64
	MemTotalBytes uint64
	NetRxBytes    uint64 // bytes received since boot, excluding lo
	NetTxBytes    uint64 // bytes sent since boot, excluding lo
	TCPConns      int    // ESTABLISHED connections on this host
}

// Sample collects the current snapshot.
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

// cpuUtilPercent returns CPU usage since the last sample; the first sample only builds the baseline and returns 0
// (cpu.Percent(0) is a differential measure, so this restores the original "first sample is 0" semantics).
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

// establishedCount counts the ESTABLISHED TCP connections on this host (v4+v6).
// it parses /proc/net/tcp{,6} directly instead of using gopsutil Connections: since v4.26.10
// that entry point reverse-maps inodes through /proc/<pid>/fd and walks the whole process table, which on
// a busy node can take seconds for a single call and would drag the heartbeat sample past its period; here only a count is needed, so reading the two proc files line by line suffices.
func establishedCount() int {
	n := 0
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		n += countEstablishedFrom(f)
		_ = f.Close()
	}
	return n
}

// countEstablishedFrom counts the state=01 (ESTABLISHED) lines of a /proc/net/tcp text stream:
// state sits in column 4 (the header and truncated lines are skipped) and fields are whitespace separated.
func countEstablishedFrom(r io.Reader) int {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[3] != "01" {
			continue
		}
		n++
	}
	return n
}
