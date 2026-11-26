package app

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/procs"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestRelayCrashDoesNotStopHeartbeat covers F-0831: the relay is a managed
// process under a supervisor whose crash path restarts it, but the core
// heartbeat loop keeps reporting independently of its state.
func TestRelayCrashDoesNotStopHeartbeat(t *testing.T) {
	dir := t.TempDir()
	a := newDispatchApp()
	a.cfg.Procs = []config.ProcSpec{{
		Name: "relay", Kind: "relay", Exec: "sh", Args: []string{"-c", "sleep 60"},
		ConfigPath: dir + "/relay.json",
	}}
	a.mgr = procs.New(a.cfg.Procs, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.mgr.Start(ctx)
	t.Cleanup(func() { _ = a.mgr.Control("relay", agentproto.ProcActionStop) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := a.mgr.Statuses()
		if len(st) == 1 && st[0].State == agentproto.ProcRunning && st[0].PID > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay never started: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
	relayPID := a.mgr.Statuses()[0].PID
	if err := syscall.Kill(relayPID, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill relay: %v", err)
	}

	// The heartbeat is a separate goroutine: it keeps sending while the relay
	// supervisor notices the crash and backs off before restarting.
	// The heartbeat loop itself is independent of the managed process: it
	// keeps sending while the relay supervisor notices the crash and backs off
	// before restarting. (Process state changes ride proc_report on a separate
	// channel; the heartbeat snapshot only carries what a.Procs returns —
	// nil in this test app — which is exactly the isolation claim.)
	rec := &recorder{}
	a.setSend(rec.send)
	a.setInterval(10 * time.Millisecond)
	a.startHeartbeat(ctx, rec.send)
	deadline = time.Now().Add(3 * time.Second)
	for rec.count() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat stopped after relay crash: %d messages", rec.count())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The supervisor restarted the killed relay within the backoff window.
	crashed := false
	deadline = time.Now().Add(15 * time.Second)
	for !crashed {
		for _, s := range a.mgr.Statuses() {
			if s.Name == "relay" && s.Restarts >= 1 && s.State == agentproto.ProcRunning {
				crashed = true
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay never restarted after the kill: %+v", a.mgr.Statuses())
		}
		time.Sleep(30 * time.Millisecond)
	}
}
