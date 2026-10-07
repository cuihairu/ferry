package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestParseMemInfo 合成 /proc/meminfo 验证 used 与占用比（P1-8）。
func TestParseMemInfo(t *testing.T) {
	data := []byte(`MemTotal:        2048 kB
MemFree:          256 kB
MemAvailable:      512 kB
Buffers:           64 kB
Cached:           128 kB
SwapTotal:         0 kB
`)
	m, err := parseMemInfo(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.TotalBytes != 2048*1024 {
		t.Fatalf("total = %d, want %d", m.TotalBytes, 2048*1024)
	}
	if want := int64(2048-512) * 1024; m.UsedBytes != want {
		t.Fatalf("used = %d, want %d", m.UsedBytes, want)
	}
	if m.UsagePercent != 75 {
		t.Fatalf("percent = %v, want 75", m.UsagePercent)
	}
	// 缺 MemAvailable：按全量算 used
	m, err = parseMemInfo([]byte("MemTotal: 100 kB\n"))
	if err != nil {
		t.Fatalf("parse minimal: %v", err)
	}
	if m.UsedBytes != 100*1024 || m.UsagePercent != 100 {
		t.Fatalf("minimal meminfo: %+v", m)
	}
	// 缺 MemTotal 报错
	if _, err := parseMemInfo([]byte("SwapTotal: 0 kB\n")); err == nil {
		t.Fatal("missing MemTotal should error")
	}
}

// TestParseLoadAvgUptimeCPU 覆盖负载/运行时长/CPU jiffies 解析。
func TestParseLoadAvgUptimeCPU(t *testing.T) {
	loads, err := parseLoadAvg([]byte("0.52 0.58 0.59 2/1234 5678\n"))
	if err != nil {
		t.Fatalf("loadavg: %v", err)
	}
	if loads[0] != 0.52 || loads[1] != 0.58 || loads[2] != 0.59 {
		t.Fatalf("loads = %v", loads)
	}
	if _, err := parseLoadAvg([]byte("0.52 0.58")); err == nil {
		t.Fatal("short loadavg should error")
	}

	up, err := parseUptime([]byte("12345.67 23456.78\n"))
	if err != nil || up != 12345 {
		t.Fatalf("uptime = %d err=%v", up, err)
	}

	// busy = total - idle - iowait：首行 user10+nice10+system10+idle70+iowait10 → busy=30, total=110
	busy, total, err := parseCPUStat([]byte("cpu  10 10 10 70 10 0 0 0 0 0\ncpu0 1 1 1 7 1 0 0 0 0 0\n"))
	if err != nil {
		t.Fatalf("cpu stat: %v", err)
	}
	if busy != 30 || total != 110 {
		t.Fatalf("busy=%v total=%v, want 30/110", busy, total)
	}
	if _, _, err := parseCPUStat([]byte("cpu0 1 1 1 1\n")); err == nil {
		t.Fatal("missing aggregate cpu line should error")
	}
}

// TestSysStatus 端到端：计数概况正确、进程与 /proc 指标齐备。
func TestSysStatus(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	for _, name := range []string{"n1", "n2"} {
		if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": "a.example.com", "port": 443, "protocol": "vless",
		}); rec.Code != http.StatusCreated {
			t.Fatalf("create node %s: %d", name, rec.Code)
		}
	}
	if err := db.Exec(`UPDATE nodes SET status='online' WHERE name='n1'`).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"u1", "u2", "u3"} {
		if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": name}); rec.Code != http.StatusCreated {
			t.Fatalf("create user %s: %d", name, rec.Code)
		}
	}
	if err := db.Exec(`UPDATE users SET enabled=false WHERE username='u3'`).Error; err != nil {
		t.Fatal(err)
	}

	w := doAdmin(t, r, "GET", "/admin/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	nodes := out["nodes"].(map[string]any)
	if nodes["total"].(float64) != 2 || nodes["online"].(float64) != 1 {
		t.Fatalf("nodes counts mismatch: %v", nodes)
	}
	users := out["users"].(map[string]any)
	if users["total"].(float64) != 3 || users["enabled"].(float64) != 2 {
		t.Fatalf("users counts mismatch: %v", users)
	}
	proc := out["process"].(map[string]any)
	if proc["alloc_bytes"].(float64) <= 0 || proc["goroutines"].(float64) <= 0 {
		t.Fatalf("process metrics mismatch: %v", proc)
	}
	if _, ok := out["uptime_sec"]; !ok {
		t.Fatal("uptime_sec missing (running on linux /proc expected)")
	}

	// 无令牌 401
	if w := doJSON(t, r, "GET", "/admin/status", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("without token should 401: %d", w.Code)
	}
}
