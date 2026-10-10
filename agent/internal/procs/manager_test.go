//go:build unix

package procs

import (
	"context"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

func testManager(t *testing.T, specs []config.ProcSpec) (*Manager, *collector) {
	t.Helper()
	m := New(specs, log.New(t.Output(), "", log.LstdFlags))
	m.minBackoff = 50 * time.Millisecond
	m.maxBackoff = 200 * time.Millisecond
	m.stableRun = time.Second
	c := &collector{mu: &sync.Mutex{}}
	m.OnStatusChange = func(s agentproto.ProcStatus) { c.add("status", s.Name+":"+s.State) }
	m.OnAlarm = func(a agentproto.Alarm) { c.add("alarm", a.Kind+":"+a.Proc+":"+a.Message) }
	return m, c
}

type collector struct {
	mu   *sync.Mutex
	rows []string
}

func (c *collector) add(kind, detail string) {
	c.mu.Lock()
	c.rows = append(c.rows, kind+"="+detail)
	c.mu.Unlock()
}

func (c *collector) has(prefix string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.rows {
		if len(r) >= len(prefix) && r[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// waitFor 等待进程进入目标状态，超时则失败。
func waitFor(t *testing.T, m *Manager, name, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		for _, s := range m.Statuses() {
			if s.Name == name {
				if s.State == want {
					return
				}
				last = s.State
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("proc %s never became %s (last=%s)", name, want, last)
}

func TestStartStopLifecycle(t *testing.T) {
	m, c := testManager(t, []config.ProcSpec{{
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
	if !c.has("status=sleeper:running") || !c.has("status=sleeper:stopped") {
		t.Fatalf("missing status events: %v", c.rows)
	}
}

func TestCrashAutoRestart(t *testing.T) {
	m, c := testManager(t, []config.ProcSpec{{
		Name: "crasher", Kind: "xray", Exec: "sh", Args: []string{"-c", "exit 1"},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// 崩溃后应自动拉起并产生告警
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, s := range m.Statuses() {
			if s.Name == "crasher" && s.Restarts >= 2 {
				goto crashed
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("restarts never reached 2")
		}
		time.Sleep(30 * time.Millisecond)
	}
crashed:
	if !c.has("alarm=proc_crash:crasher") {
		t.Fatalf("missing crash alarm: %v", c.rows)
	}

	// stop 后不再拉起
	if err := m.Control("crasher", agentproto.ProcActionStop); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, m, "crasher", agentproto.ProcStopped)
	time.Sleep(300 * time.Millisecond)
	for _, s := range m.Statuses() {
		if s.Name == "crasher" && s.State == agentproto.ProcRunning {
			t.Fatal("must not relaunch after stop")
		}
	}
}

func TestReloadRestartStrategy(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "svc", Kind: "xray", Exec: "sh", Args: []string{"-c", "sleep 60"},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	waitFor(t, m, "svc", agentproto.ProcRunning)
	oldPID := pidOf(t, m, "svc")
	if err := m.Control("svc", agentproto.ProcActionReload); err != nil {
		t.Fatalf("reload: %v", err)
	}
	// reload（restart 策略）后进程必须是新实例：running 且 pid 变化
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pid := pidOf(t, m, "svc")
		if pid != 0 && pid != oldPID {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("reload did not replace process (old pid=%d)", oldPID)
}

// pidOf 返回进程当前 pid，未运行为 0。
func pidOf(t *testing.T, m *Manager, name string) int {
	t.Helper()
	for _, s := range m.Statuses() {
		if s.Name == name && s.State == agentproto.ProcRunning {
			return s.PID
		}
	}
	return 0
}

func TestControlErrors(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "svc", Kind: "xray", Exec: "sh", Args: []string{"-c", "sleep 60"},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	waitFor(t, m, "svc", agentproto.ProcRunning)

	if err := m.Control("nope", agentproto.ProcActionStart); err == nil {
		t.Fatal("unknown proc must error")
	}
	if err := m.Control("svc", "dance"); err == nil {
		t.Fatal("unknown action must error")
	}
	if err := m.Control("svc", agentproto.ProcActionStart); err == nil {
		t.Fatal("start on running must error")
	}
}

// TestProcLogs 覆盖进程日志拉取（P1-11）：stdout 镜像进环形缓冲、
// limit 取最近 N 行、未知进程报错。
func TestProcLogs(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "logger", Kind: "xray", Exec: "sh", Args: []string{"-c", "echo line-1; echo line-2; sleep 60"},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	waitFor(t, m, "logger", agentproto.ProcRunning)

	// 等待两行日志进入环形缓冲
	deadline := time.Now().Add(3 * time.Second)
	var lines []string
	for time.Now().Before(deadline) {
		lines, _ = m.Logs("logger", 0)
		if len(lines) >= 2 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if len(lines) != 2 || lines[0] != "line-1" || lines[1] != "line-2" {
		t.Fatalf("proc logs = %v", lines)
	}
	// limit 取最近 N 行
	if lines, _ = m.Logs("logger", 1); len(lines) != 1 || lines[0] != "line-2" {
		t.Fatalf("limited proc logs = %v", lines)
	}
	if _, err := m.Logs("nope", 10); err == nil {
		t.Fatal("unknown proc must error")
	}
}

// waitForAlarm 等待出现前缀匹配的告警（消息含 exec 路径等细节），超时失败。
func waitForAlarm(t *testing.T, c *collector, prefix string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.has(prefix) {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t.Fatalf("alarm %q never fired (rows=%v)", prefix, c.rows)
}

// TestBinaryMissingAlarm 覆盖二进制缺失面（hy2 批）：启动预检显式告警
// （面板可见明确原因而非静默），监管循环退避重试、装上二进制前不误报
// running；launch 失败告警带 exec 路径。
func TestBinaryMissingAlarm(t *testing.T) {
	m, c := testManager(t, []config.ProcSpec{{
		Name: "hy2", Kind: "hysteria2", Exec: "/nonexistent/ferry-test-hysteria",
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// 预检告警：exec 路径进消息（面板给明确提示而不是静默失败）。
	waitForAlarm(t, c, "alarm=proc_crash:hy2:binary not found: /nonexistent/ferry-test-hysteria")
	// launch 失败告警同样带 exec 路径与底层错误。
	waitForAlarm(t, c, "alarm=proc_crash:hy2:launch failed: exec /nonexistent/ferry-test-hysteria:")
	// 进程从未 running。
	for _, s := range m.Statuses() {
		if s.Name == "hy2" && s.State == agentproto.ProcRunning {
			t.Fatal("missing binary must not report running")
		}
	}
}
