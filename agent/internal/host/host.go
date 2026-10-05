// Package host 读取本机负载与内存快照，数据源为 /proc，读取失败返回零值不报错。
package host

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var bootTime = time.Now()

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
	rx, tx := netDevBytes(readFile("/proc/net/dev"))
	return Snapshot{
		UptimeSec:     uptime(),
		Load1:         load1(),
		CPUUtil:       cpuUtilPercent(),
		MemUsedBytes:  memUsed(),
		MemTotalBytes: memTotal(),
		NetRxBytes:    rx,
		NetTxBytes:    tx,
		TCPConns:      establishedCount(readFile("/proc/net/tcp")) + establishedCount(readFile("/proc/net/tcp6")),
	}
}

func readFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func uptime() int64 {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return int64(time.Since(bootTime).Seconds())
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0
	}
	sec, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return int64(sec)
}

func load1() float64 {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return v
}

// meminfo 解析出 total 与 available（KB 单位），缺失项为 0。
func meminfo() (total, available uint64) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := cutKV(line)
		if !ok {
			continue
		}
		switch k {
		case "MemTotal":
			total = v
		case "MemAvailable":
			available = v
		}
	}
	return total, available
}

func memUsed() uint64 {
	total, avail := meminfo()
	if total == 0 || avail > total {
		return 0
	}
	return (total - avail) * 1024
}

func memTotal() uint64 {
	total, _ := meminfo()
	return total * 1024
}

// cutKV 从 "MemTotal:       16308180 kB" 解析出键与 KB 值。
func cutKV(line string) (string, uint64, bool) {
	k, v, found := strings.Cut(line, ":")
	if !found {
		return "", 0, false
	}
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return "", 0, false
	}
	n, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return strings.TrimSpace(k), n, true
}

// cpuStat 从 /proc/stat 首行解析总 tick 与空闲 tick（含 iowait）。
func cpuStat(content string) (total, idle uint64, ok bool) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return 0, 0, false
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for _, f := range fields[1:] {
		n, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += n
	}
	idle, err := strconv.ParseUint(fields[4], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if len(fields) > 5 { // iowait 计入空闲
		if w, err := strconv.ParseUint(fields[5], 10, 64); err == nil {
			idle += w
		}
	}
	return total, idle, true
}

// cpuUtilBetween 计算两段采样间的 CPU 使用率 0-100。
func cpuUtilBetween(prevTotal, prevIdle, total, idle uint64) float64 {
	dt := total - prevTotal
	di := idle - prevIdle
	if dt == 0 || di > dt {
		return 0
	}
	return 100 * float64(dt-di) / float64(dt)
}

var cpuPrev struct {
	mu    sync.Mutex
	valid bool
	total uint64
	idle  uint64
}

// cpuUtilPercent 返回自上次采样以来的 CPU 使用率；首次采样建立基线返回 0。
func cpuUtilPercent() float64 {
	total, idle, ok := cpuStat(readFile("/proc/stat"))
	if !ok {
		return 0
	}
	cpuPrev.mu.Lock()
	defer cpuPrev.mu.Unlock()
	if !cpuPrev.valid {
		cpuPrev.valid, cpuPrev.total, cpuPrev.idle = true, total, idle
		return 0
	}
	util := cpuUtilBetween(cpuPrev.total, cpuPrev.idle, total, idle)
	cpuPrev.total, cpuPrev.idle = total, idle
	return util
}

// netDevBytes 从 /proc/net/dev 内容汇总收发字节（排除 lo 回环）。
func netDevBytes(content string) (rx, tx uint64) {
	for _, line := range strings.Split(content, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(name) == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 16 {
			continue
		}
		if r, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
			rx += r
		}
		if t, err := strconv.ParseUint(fields[8], 10, 64); err == nil {
			tx += t
		}
	}
	return rx, tx
}

// establishedCount 从 /proc/net/tcp 内容统计 ESTABLISHED（状态 01）连接数。
func establishedCount(content string) int {
	n := 0
	for _, line := range strings.Split(content, "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[3] == "01" {
			n++
		}
	}
	return n
}
