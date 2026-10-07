package handler

import (
	"errors"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 系统状态（P1-8，对齐 3x-ui server/status 的内存/CPU/负载口径）：
// 直接解析 /proc，不引入 gopsutil（小内存 VPS 零依赖）。

// memUsage 是系统内存用量。
type memUsage struct {
	TotalBytes   int64   `json:"total_bytes"`
	UsedBytes    int64   `json:"used_bytes"`
	UsagePercent float64 `json:"usage_percent"`
}

// parseMemInfo 解析 /proc/meminfo：used = total - available（含可回收缓存口径）。
func parseMemInfo(data []byte) (memUsage, error) {
	var total, available int64
	haveTotal, haveAvail := false, false
	for _, line := range strings.Split(string(data), "\n") {
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(name) {
		case "MemTotal":
			total, haveTotal = kb*1024, true
		case "MemAvailable":
			available, haveAvail = kb*1024, true
		}
	}
	if !haveTotal {
		return memUsage{}, errors.New("MemTotal not found")
	}
	used := total
	if haveAvail {
		used = total - available
	}
	if used < 0 {
		used = 0
	}
	return memUsage{
		TotalBytes:   total,
		UsedBytes:    used,
		UsagePercent: percent(used, total),
	}, nil
}

// parseLoadAvg 解析 /proc/loadavg 的 1/5/15 分钟负载。
func parseLoadAvg(data []byte) ([]float64, error) {
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return nil, errors.New("loadavg too short")
	}
	out := make([]float64, 3)
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// parseUptime 解析 /proc/uptime（秒）。
func parseUptime(data []byte) (int64, error) {
	sec, _, found := strings.Cut(strings.TrimSpace(string(data)), " ")
	if !found {
		return 0, errors.New("uptime too short")
	}
	v, err := strconv.ParseFloat(sec, 64)
	if err != nil {
		return 0, err
	}
	return int64(v), nil
}

// parseCPUStat 解析 /proc/stat 首行 cpu 汇总，返回 busy/total 累计 jiffies。
func parseCPUStat(data []byte) (busy, total float64, err error) {
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		if len(fields) < 4 {
			return 0, 0, errors.New("cpu stat too short")
		}
		var idle, iowait float64
		for i, f := range fields {
			v, perr := strconv.ParseFloat(f, 64)
			if perr != nil {
				return 0, 0, perr
			}
			total += v
			switch i {
			case 3: // idle
				idle = v
			case 4: // iowait
				iowait = v
			}
		}
		return total - idle - iowait, total, nil
	}
	return 0, 0, errors.New("cpu stat line not found")
}

// cpuPercent 两次采样计算窗口内 CPU 占用百分比。
func cpuPercent(sample time.Duration) (float64, error) {
	data1, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}
	busy1, total1, err := parseCPUStat(data1)
	if err != nil {
		return 0, err
	}
	time.Sleep(sample)
	data2, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}
	busy2, total2, err := parseCPUStat(data2)
	if err != nil {
		return 0, err
	}
	if total2 <= total1 {
		return 0, errors.New("cpu stat not advancing")
	}
	return (busy2 - busy1) / (total2 - total1) * 100, nil
}

func percent(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}

// sysStatus 面板系统状态（GET /admin/status）：内存/负载/运行时长/CPU/
// ferry 进程用量 + 节点与用户在线概况；/proc 缺失的项缺省（不阻断）。
func (h *Handler) sysStatus(c *gin.Context) {
	out := gin.H{"now": time.Now()}

	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		if m, err := parseMemInfo(data); err == nil {
			out["memory"] = m
		}
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		if l, err := parseLoadAvg(data); err == nil {
			out["loads"] = l
		}
	}
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if u, err := parseUptime(data); err == nil {
			out["uptime_sec"] = u
		}
	}
	if p, err := cpuPercent(150 * time.Millisecond); err == nil {
		out["cpu_percent"] = p
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	out["process"] = gin.H{
		"alloc_bytes": ms.Alloc,
		"sys_bytes":   ms.Sys,
		"goroutines":  runtime.NumGoroutine(),
	}

	var nodesTotal, nodesOnline, usersTotal, usersEnabled int64
	if err := h.db.Model(&storage.Node{}).Count(&nodesTotal).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.Model(&storage.Node{}).Where("status = ?", "online").Count(&nodesOnline).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.Model(&storage.User{}).Count(&usersTotal).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.Model(&storage.User{}).Where("enabled = ?", true).Count(&usersEnabled).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out["nodes"] = gin.H{"total": nodesTotal, "online": nodesOnline}
	out["users"] = gin.H{"total": usersTotal, "enabled": usersEnabled}

	c.JSON(http.StatusOK, out)
}
