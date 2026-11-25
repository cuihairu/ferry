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

// TestNewNilLoggerAndSilentAlarm covers a nil logger and no OnAlarm callback: neither construction nor alarm
// dispatching may panic (an alarm with no subscriber returns silently).
func TestNewNilLoggerAndSilentAlarm(t *testing.T) {
	m := New(nil, nil)
	if m.log == nil {
		t.Fatal("New must default the logger")
	}
	m.OnAlarm = nil
	m.OnStatusChange = nil
	m.alarm(&proc{spec: config.ProcSpec{Name: "x"}}, agentproto.AlarmKindProcCrash, "critical", "boom")
}

// TestControlStartFromStopped covers the stopped-state command round trip: after stop (the stopped supervision branch),
// a start brings the process back to running -- covering the command handling path of the stopped-state select.
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

// TestControlStoppedReloadAndUnknownAction covers reload while stopped being equivalent to start;
// an unknown name (delivered directly, bypassing Control validation) returns an error -- the full applyWhenStopped branch.
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
	// deliver an illegal action straight to the stopped supervision branch (Control-level validation would reject it first, so
	// this punches through the white box to cover applyWhenStopped's fallback branch).
	m.mu.Lock()
	p := m.procs["sleeper"]
	m.mu.Unlock()
	reply := make(chan error, 1)
	p.req <- ctrlReq{action: "bogus", reply: reply}
	if err := <-reply; err == nil {
		t.Fatal("bogus action must error in applyWhenStopped")
	}
}

// TestReloadSignalSendsHUP covers the signal policy reload: after signalling a process that ignores HUP,
// the process is still running and the reload succeeds.
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
	waitFor(t, m, "sleeper", agentproto.ProcRunning) // not killed by the HUP
}

// TestCrashBackoffCap covers the crash backoff cap: a repeatedly crashing process doubles its backoff up to maxBackoff and
// then holds at the cap, with restarts continuing.
func TestCrashBackoffCap(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "crasher", Kind: "xray", Exec: "sh", Args: []string{"-c", "exit 1"},
	}})
	m.minBackoff = 10 * time.Millisecond
	m.maxBackoff = 40 * time.Millisecond
	m.stableRun = time.Hour // never considered a stable run, so the backoff keeps doubling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for {
		st := m.Statuses()
		if len(st) == 1 && st[0].Restarts >= 4 { // 10 -> 20 -> 40 -> capped at 40 for at least 4 rounds
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("crash loop stalled: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestCollectProcMetricsFailure covers metric collection failures: an invalid URL and an unreachable endpoint both
// clear the snapshot and keep polling (no panic, no impact on process supervision).
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
	// collection failed -> Metrics is nil; still no snapshot after a few polling periods is the expected outcome
	time.Sleep(60 * time.Millisecond)
	for _, s := range m.Statuses() {
		if s.Metrics != nil {
			t.Fatalf("proc %s metrics = %v; want nil on collect failure", s.Name, s.Metrics)
		}
	}
}

// TestMetricsCollectSuccess covers successful metric collection: a local HTTP endpoint returns JSON metrics,
// and the snapshot is exposed through Statuses.
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
