//go:build unix

package procs

import (
	"context"
	"log"
	"sync"
	"syscall"
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

// all returns a snapshot of the lines for Fatalf to print (rows must not be read raw; the read loop appends from a background goroutine).
func (c *collector) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.rows...)
}

// waitFor waits for the process to reach the target state, failing on timeout.
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

// waitForEvent waits for the state event to land in the collector: setState delivers through a separate goroutine,
// so when waitFor (which reads synchronized state) returns the event may not have arrived yet; wait for the event before asserting.
func waitForEvent(t *testing.T, c *collector, prefix string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.has(prefix) {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("event %q never fired (rows=%v)", prefix, c.all())
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
	waitForEvent(t, c, "status=sleeper:stopped")
	if !c.has("status=sleeper:running") || !c.has("status=sleeper:stopped") {
		t.Fatalf("missing status events: %v", c.all())
	}
}

func TestCrashAutoRestart(t *testing.T) {
	m, c := testManager(t, []config.ProcSpec{{
		Name: "crasher", Kind: "xray", Exec: "sh", Args: []string{"-c", "exit 1"},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// it should be restarted automatically after the crash and raise an alarm
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
		t.Fatalf("missing crash alarm: %v", c.all())
	}

	// no restart after stop
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

// TestCrashDetectionWithLingeringGrandchild reproduces the "child killed externally while a grandchild
// still holds the stdio pipe" scenario: after the direct child exits, cmd.Wait still waits for the pipe to close,
// while the pipe write end belongs to the grandchild (nginx workers, daemonizing kernels all do this),
// so the supervision loop hangs and crash detection plus auto-restart stall entirely. Wait must be deadline-bounded.
func TestCrashDetectionWithLingeringGrandchild(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "forker", Kind: "xray",
		Exec: "sh", Args: []string{"-c", "echo up; sleep 60"},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	waitFor(t, m, "forker", agentproto.ProcRunning)
	pid := 0
	for _, s := range m.Statuses() {
		if s.Name == "forker" {
			pid = s.PID
		}
	}
	if pid == 0 {
		t.Fatal("forker has no pid")
	}
	// kill only the direct child: the sleep 60 grandchild survives and holds the stdio pipe write end.
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("kill: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range m.Statuses() {
			if s.Name == "forker" && s.Restarts >= 1 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("crash not detected within 5s (grandchild holding stdio pipe stalls Wait)")
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
	// after a reload (restart policy) the process must be a new instance: running with a changed pid
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

// pidOf returns the process's current pid, or 0 when it is not running.
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

// TestProcLogs covers process log fetching (P1-11): stdout mirrored into the ring buffer,
// limit taking the most recent N lines, and unknown processes erroring.
func TestProcLogs(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "logger", Kind: "xray", Exec: "sh", Args: []string{"-c", "echo line-1; echo line-2; sleep 60"},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	waitFor(t, m, "logger", agentproto.ProcRunning)

	// wait for both log lines to enter the ring buffer
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
	// limit takes the most recent N lines
	if lines, _ = m.Logs("logger", 1); len(lines) != 1 || lines[0] != "line-2" {
		t.Fatalf("limited proc logs = %v", lines)
	}
	if _, err := m.Logs("nope", 10); err == nil {
		t.Fatal("unknown proc must error")
	}
}

// waitForAlarm waits for an alarm matching the prefix (the message carries the exec path and other details), failing on timeout.
func waitForAlarm(t *testing.T, c *collector, prefix string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.has(prefix) {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("alarm %q never fired (rows=%v)", prefix, c.all())
}

// TestBinaryMissingAlarm covers the missing-binary surface (hy2 batch): the startup precheck alarms explicitly
// (the panel sees a clear reason instead of silence), the supervision loop retries with backoff and does not falsely report
// running before the binary is installed; the launch-failure alarm carries the exec path.
func TestBinaryMissingAlarm(t *testing.T) {
	m, c := testManager(t, []config.ProcSpec{{
		Name: "hy2", Kind: "hysteria2", Exec: "/nonexistent/ferry-test-hysteria",
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	// precheck alarm: the exec path goes into the message (a clear panel hint instead of a silent failure).
	waitForAlarm(t, c, "alarm=proc_crash:hy2:binary not found: /nonexistent/ferry-test-hysteria")
	// the launch-failure alarm also carries the exec path and the underlying error.
	waitForAlarm(t, c, "alarm=proc_crash:hy2:launch failed: exec /nonexistent/ferry-test-hysteria:")
	// the process was never running.
	for _, s := range m.Statuses() {
		if s.Name == "hy2" && s.State == agentproto.ProcRunning {
			t.Fatal("missing binary must not report running")
		}
	}
}
