package probe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestNewDefaultsLogger covers a nil logger: the default logger is used and the constructor stays usable.
func TestNewDefaultsLogger(t *testing.T) {
	r := New(nil, nil)
	if r == nil || r.log == nil {
		t.Fatal("New must default the logger")
	}
}

// TestProbeOnceWithoutSend covers being offline (no sender set): the conclusion is dropped silently without an error.
func TestProbeOnceWithoutSend(t *testing.T) {
	r := New([]config.ProbeSpec{{Name: "quiet"}}, log.New(io.Discard, "", 0))
	r.probeOnce(config.ProbeSpec{Name: "quiet", Target: "127.0.0.1:1"}) // the send == nil branch
}

// TestProbeOnceSendFailure covers a failed report: log it, no retry, no panic.
func TestProbeOnceSendFailure(t *testing.T) {
	var buf bytes.Buffer
	r := New(nil, log.New(&buf, "", 0))
	r.SetSend(func(agentproto.Envelope) error { return errors.New("link down") })
	r.probeOnce(config.ProbeSpec{Name: "flaky", Target: "127.0.0.1:1"})
	if !bytes.Contains(buf.Bytes(), []byte("probe flaky report")) {
		t.Fatalf("send failure not logged: %q", buf.String())
	}
}

// TestNextIntervalDefaultsZero covers an unset interval (<=0): fall back to the 30s default.
func TestNextIntervalDefaultsZero(t *testing.T) {
	spec := config.ProbeSpec{Direction: agentproto.DirectionOut, IntervalSec: 0}
	if got := nextInterval(spec, time.Now()); got != 30*time.Second {
		t.Fatalf("nextInterval = %v; want 30s default", got)
	}
}

// TestNextIntervalBoosted covers the F-0925 fault speed-up: sick specs
// temporarily probe faster, the gap is floored and capped so the boost can
// never backfire, and the cadence decays back once the window passes.
func TestNextIntervalBoosted(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Second)      // boost expired
	future := now.Add(sickBoostWindow) // boost active
	specs := []config.ProbeSpec{
		{Direction: agentproto.DirectionOut, IntervalSec: 3600},
		{Direction: agentproto.DirectionOut, IntervalSec: 60},
		{Direction: agentproto.DirectionOut, IntervalSec: 5},
	}
	// no boost: exactly nextInterval
	for _, spec := range specs {
		if got := nextIntervalBoosted(spec, now, past); got != nextInterval(spec, now) {
			t.Fatalf("expired boost must not change the interval: %v", got)
		}
	}
	// active boost: 3600s -> 900s, 60s -> 15s
	if got := nextIntervalBoosted(specs[0], now, future); got != 900*time.Second {
		t.Fatalf("boosted 3600s interval = %v; want 900s", got)
	}
	if got := nextIntervalBoosted(specs[1], now, future); got != 15*time.Second {
		t.Fatalf("boosted 60s interval = %v; want 15s", got)
	}
	// tiny interval: the 10s floor would exceed the interval, the cap wins
	if got := nextIntervalBoosted(specs[2], now, future); got != 5*time.Second {
		t.Fatalf("boosted 5s interval = %v; want 5s (cap over floor)", got)
	}
	// medium interval: 30s/4 = 7.5s, floored to 10s
	spec := config.ProbeSpec{Direction: agentproto.DirectionOut, IntervalSec: 30}
	if got := nextIntervalBoosted(spec, now, future); got != 10*time.Second {
		t.Fatalf("boosted 30s interval = %v; want 10s (floor)", got)
	}
}

// TestRunnerReplace covers the F-0921 hot swap: Replace before Run swaps the
// startup set; Replace while running stops the old loop for good (no further
// reports from the withdrawn spec) and the new set probes right away.
func TestRunnerReplace(t *testing.T) {
	accept := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
		return ln.Addr().String()
	}
	addrA, addrB := accept(), accept()

	specA := config.ProbeSpec{Name: "task-a", TargetKind: agentproto.ProbeTargetTunnel, Target: addrA, IntervalSec: 5}
	specB := config.ProbeSpec{Name: "task-b", TargetKind: agentproto.ProbeTargetTunnel, Target: addrB, IntervalSec: 5}

	r := New([]config.ProbeSpec{specA}, log.New(io.Discard, "", 0))
	r.Replace([]config.ProbeSpec{specB})
	if got := r.Specs(); len(got) != 1 || got[0].Name != "task-b" {
		t.Fatalf("pre-run Replace: Specs() = %+v; want task-b", got)
	}

	c := &collect{}
	r.SetSend(c.send)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.SetStartJitter(0) // keep the test timing deterministic
	go r.Run(ctx)

	// The swapped-in set starts probing immediately: the first report is task-b.
	waitFor(t, c, addrB)

	// Swap to task-a while running: task-a probes once, and task-b -- withdrawn
	// now -- must never report again. Its old loop would re-probe 5s after its
	// first round, so holding the window past that point proves the stop.
	r.Replace([]config.ProbeSpec{specA})
	waitFor(t, c, addrA)

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	counts := map[string]int{}
	for _, rep := range c.all() {
		counts[rep.TargetHost]++
	}
	if counts[addrB] != 1 {
		t.Fatalf("withdrawn task-b reported %d times; want exactly 1 (old loop must stop)", counts[addrB])
	}
	if counts[addrA] < 1 {
		t.Fatalf("task-a reported %d times; want at least 1", counts[addrA])
	}
}

// TestStartJitterSpread covers the F-0926 anti-storm spread: delays stay in
// [0, min(window, interval)], they really spread across calls, and 0 disables
// the spread entirely.
func TestStartJitterSpread(t *testing.T) {
	spec := config.ProbeSpec{Direction: agentproto.DirectionOut, IntervalSec: 3600}
	r := New(nil, nil) // default window
	max := time.Duration(0)
	for i := 0; i < 200; i++ {
		d := r.startDelay(spec)
		if d < 0 || d > stormJitterWindow {
			t.Fatalf("startDelay %v outside the window", d)
		}
		if d > max {
			max = d
		}
	}
	if max < stormJitterWindow/4 {
		t.Fatalf("delays never spread (max %v); want a real spread", max)
	}
	// the spec's own interval caps the window: a 5s spec never waits longer
	small := config.ProbeSpec{Direction: agentproto.DirectionOut, IntervalSec: 5}
	for i := 0; i < 100; i++ {
		if d := r.startDelay(small); d > 5*time.Second {
			t.Fatalf("startDelay %v exceeds the spec interval", d)
		}
	}
	// 0 disables the spread
	r.SetStartJitter(0)
	if d := r.startDelay(spec); d != 0 {
		t.Fatalf("disabled jitter produced %v; want 0", d)
	}
}

// waitFor blocks until the runner reports for target addr.
func waitFor(t *testing.T, c *collect, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, rep := range c.all() {
			if rep.TargetHost == addr {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no probe report for %s", addr)
}

// slowUDPResponder is a UDP responder that holds every round for hold before
// replying and tracks how many rounds were in flight at once.
type slowUDPResponder struct {
	addr   net.Addr
	conn   net.PacketConn
	active int32 // rounds currently in flight
	peak   int32 // high-water mark of concurrent rounds
}

func newSlowUDPResponder(t *testing.T, hold time.Duration) *slowUDPResponder {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("udp listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	s := &slowUDPResponder{addr: conn.LocalAddr(), conn: conn}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			// serve each round from its own goroutine: the responder itself must
			// never serialize overlapping rounds, that is what is being measured
			pkt := append([]byte{}, buf[:n]...) // own copy: buf is reused by ReadFrom
			go func(pkt []byte, from net.Addr) {
				cur := atomic.AddInt32(&s.active, 1)
				for {
					old := atomic.LoadInt32(&s.peak)
					if old >= cur || atomic.CompareAndSwapInt32(&s.peak, old, cur) {
						break
					}
				}
				time.Sleep(hold)
				if _, err := conn.WriteTo(pkt, from); err != nil {
					return
				}
				atomic.AddInt32(&s.active, -1)
			}(pkt, from)
		}
	}()
	return s
}

// TestProbeConcurrencyLimit covers the F-0922 cap: with a limit of 1, rounds
// from parallel specs never overlap at the target; without the cap (default),
// parallel specs do overlap.
func TestProbeConcurrencyLimit(t *testing.T) {
	specsFor := func(addr net.Addr) []config.ProbeSpec {
		var specs []config.ProbeSpec
		for _, name := range []string{"p1", "p2", "p3"} {
			specs = append(specs, config.ProbeSpec{
				Name: name, TargetKind: agentproto.ProbeTargetExit,
				Target: addr.String(), Proto: "udp", PayloadHex: "aa", IntervalSec: 3600,
			})
		}
		return specs
	}
	runAll := func(r *Runner) *collect {
		c := &collect{}
		r.SetSend(c.send)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		r.SetStartJitter(0) // keep the test timing deterministic
		go r.Run(ctx)
		return c
	}

	// capped: strictly one round in flight
	res := newSlowUDPResponder(t, 150*time.Millisecond)
	r := New(specsFor(res.addr), log.New(io.Discard, "", 0))
	r.SetConcurrency(1)
	if r.Concurrency() != 1 {
		t.Fatalf("Concurrency() = %d; want 1", r.Concurrency())
	}
	c := runAll(r)
	deadline := time.Now().Add(15 * time.Second)
	for len(c.all()) < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel := func() {} // Run keeps running until ctx cancels via t.Cleanup
	_ = cancel
	if got := atomic.LoadInt32(&res.peak); got != 1 {
		t.Fatalf("capped peak concurrent rounds = %d; want 1", got)
	}

	// default cap: three parallel specs overlap at the target
	res2 := newSlowUDPResponder(t, 150*time.Millisecond)
	r2 := New(specsFor(res2.addr), log.New(io.Discard, "", 0))
	c2 := runAll(r2)
	deadline = time.Now().Add(15 * time.Second)
	for len(c2.all()) < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&res2.peak); got < 2 {
		t.Fatalf("uncapped peak concurrent rounds = %d; want >= 2", got)
	}
}
