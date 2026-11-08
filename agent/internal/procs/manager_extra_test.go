//go:build unix

package procs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestNewNilLoggerAndSilentAlarm 缺省 logger 与无 OnAlarm 回调：构造与告警
// 分发都不得 panic（alarm 无订阅者时静默返回）。
func TestNewNilLoggerAndSilentAlarm(t *testing.T) {
	m := New(nil, nil)
	if m.log == nil {
		t.Fatal("New must default the logger")
	}
	m.OnAlarm = nil
	m.OnStatusChange = nil
	m.alarm(&proc{spec: config.ProcSpec{Name: "x"}}, agentproto.AlarmKindProcCrash, "critical", "boom")
}

// TestControlStartFromStopped 停机态指令闭环：stop 后（停机监管分支）再
// start，进程恢复运行——覆盖停机态 select 的指令处理路径。
func TestControlStartFromStopped(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "sleeper", Kind: "xray", Exec: "sh", Args: []string{"-c", "sleep 60"},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	waitFor(t, m, "sleeper", agentproto.ProcRunning)
	if err := m.Control("sleeper", agentproto.ProcActionStop); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, m, "sleeper", agentproto.ProcStopped)
	if err := m.Control("sleeper", agentproto.ProcActionStart); err != nil {
		t.Fatalf("start from stopped: %v", err)
	}
	waitFor(t, m, "sleeper", agentproto.ProcRunning)
}

// TestControlStoppedReloadAndUnknownAction 停机态 reload 等价 start；
// 未知名（直接投递绕过 Control 校验）回错误——applyWhenStopped 的完整分支。
func TestControlStoppedReloadAndUnknownAction(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "sleeper", Kind: "xray", Exec: "sh", Args: []string{"-c", "sleep 60"},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	waitFor(t, m, "sleeper", agentproto.ProcRunning)
	if err := m.Control("sleeper", agentproto.ProcActionStop); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, m, "sleeper", agentproto.ProcStopped)
	if err := m.Control("sleeper", agentproto.ProcActionReload); err != nil {
		t.Fatalf("reload from stopped (same as start): %v", err)
	}
	waitFor(t, m, "sleeper", agentproto.ProcRunning)

	if err := m.Control("sleeper", agentproto.ProcActionStop); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, m, "sleeper", agentproto.ProcStopped)
	// 直接向停机监管分支投递非法 action（Control 层校验会先行拒绝，这里
	// 白盒穿透以覆盖 applyWhenStopped 的兜底分支）。
	m.mu.Lock()
	p := m.procs["sleeper"]
	m.mu.Unlock()
	reply := make(chan error, 1)
	p.req <- ctrlReq{action: "bogus", reply: reply}
	if err := <-reply; err == nil {
		t.Fatal("bogus action must error in applyWhenStopped")
	}
}

// TestReloadSignalSendsHUP signal 策略 reload：对忽略 HUP 的进程发信号后
// 进程仍在运行且 reload 成功。
func TestReloadSignalSendsHUP(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "sleeper", Kind: "xray", Exec: "sh",
		Args:   []string{"-c", "trap \"\" HUP; sleep 60"},
		Reload: ReloadSignal,
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	waitFor(t, m, "sleeper", agentproto.ProcRunning)
	if err := m.Control("sleeper", agentproto.ProcActionReload); err != nil {
		t.Fatalf("signal reload: %v", err)
	}
	waitFor(t, m, "sleeper", agentproto.ProcRunning) // 未被 HUP 杀掉
}

// TestCrashBackoffCap 崩溃退避封顶：连崩进程的退避翻倍到 maxBackoff 后
// 保持封顶值，重启持续发生。
func TestCrashBackoffCap(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "crasher", Kind: "xray", Exec: "sh", Args: []string{"-c", "exit 1"},
	}})
	m.minBackoff = 10 * time.Millisecond
	m.maxBackoff = 40 * time.Millisecond
	m.stableRun = time.Hour // 永不视为稳定运行，退避一路翻倍
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for {
		st := m.Statuses()
		if len(st) == 1 && st[0].Restarts >= 4 { // 10→20→40→封顶 40 至少 4 轮
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("crash loop stalled: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCollectProcMetricsFailure 指标采集失败面：URL 非法与端点不可达都
// 清空快照继续轮询（不 panic、不影响进程监管）。
func TestCollectProcMetricsFailure(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{
		{Name: "badurl", Kind: "xray", Exec: "sh", Args: []string{"-c", "sleep 60"}, MetricsURL: "http://127.0.0.1:9/metr\nic"},
		{Name: "down", Kind: "xray", Exec: "sh", Args: []string{"-c", "sleep 60"}, MetricsURL: "http://127.0.0.1:9/metrics"},
	})
	m.metricsInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	waitFor(t, m, "badurl", agentproto.ProcRunning)
	waitFor(t, m, "down", agentproto.ProcRunning)
	// 采集失败 → Metrics 为 nil；给几个轮询周期后仍无快照即符合预期
	time.Sleep(60 * time.Millisecond)
	for _, s := range m.Statuses() {
		if s.Metrics != nil {
			t.Fatalf("proc %s metrics = %v; want nil on collect failure", s.Name, s.Metrics)
		}
	}
}

// TestMetricsCollectSuccess 指标采集成功面：本地 HTTP 端点返回 JSON 指标，
// 快照随 Statuses 暴露。
func TestMetricsCollectSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rx_bytes":1234,"tx_bytes":5678}`))
	}))
	defer srv.Close()

	m, _ := testManager(t, []config.ProcSpec{{
		Name: "xray", Kind: "xray", Exec: "sh", Args: []string{"-c", "sleep 60"},
		MetricsURL: srv.URL,
	}})
	m.metricsInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for {
		st := m.Statuses()
		if len(st) == 1 && st[0].Metrics != nil {
			if st[0].Metrics["rx_bytes"] != 1234 {
				t.Fatalf("metrics = %+v; want rx_bytes=1234", st[0].Metrics)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("metrics never collected: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
