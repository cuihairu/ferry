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
	m.OnAlarm = func(a agentproto.Alarm) { c.add("alarm", a.Kind+":"+a.Proc) }
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
