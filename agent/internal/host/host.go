// Package host 读取本机负载与内存快照，数据源为 /proc，读取失败返回零值不报错。
package host

import (
	"os"
	"strconv"
	"strings"
	"time"
)

var bootTime = time.Now()

// Snapshot 是一次本机状态采样。
type Snapshot struct {
	UptimeSec     int64
	Load1         float64
	MemUsedBytes  uint64
	MemTotalBytes uint64
}

// Sample 采集当前快照。
func Sample() Snapshot {
	return Snapshot{
		UptimeSec:     uptime(),
		Load1:         load1(),
		MemUsedBytes:  memUsed(),
		MemTotalBytes: memTotal(),
	}
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
