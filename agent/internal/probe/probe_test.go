package probe

import (
	"context"
	"io"
	"log"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// collect is a sender that gathers reported conclusions.
type collect struct {
	mu      sync.Mutex
	reports []agentproto.ProbeReport
}

func (c *collect) send(env agentproto.Envelope) error {
	var batch agentproto.ProbeReportBatch
	if err := env.Decode(&batch); err != nil {
		return err
	}
	c.mu.Lock()
	c.reports = append(c.reports, batch.Items...)
	c.mu.Unlock()
	return nil
}

func (c *collect) all() []agentproto.ProbeReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]agentproto.ProbeReport, len(c.reports))
	copy(out, c.reports)
	return out
}

// waitReport waits for the first reported conclusion.
func waitReport(t *testing.T, c *collect) []agentproto.ProbeReport {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rs := c.all(); len(rs) > 0 {
			return rs
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no probe report received")
	return nil
}

func TestProbeHealthy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	specs := []config.ProbeSpec{{
		Name: "exit", TargetKind: agentproto.ProbeTargetExit,
		Target: ln.Addr().String(), Direction: agentproto.DirectionOut,
		IntervalSec: 5,
	}}
	r := New(specs, log.New(io.Discard, "", 0))
	c := &collect{}
	r.SetSend(c.send)

	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	defer cancel()

	rs := waitReport(t, c)
	got := rs[0]
	if got.Verdict != agentproto.ProbeVerdictHealthy || !got.Reachable {
		t.Fatalf("healthy expected: %+v", got)
	}
	if got.RttMs < 0 {
		t.Fatalf("rtt = %d", got.RttMs)
	}
	if got.TargetKind != agentproto.ProbeTargetExit || got.TargetHost != ln.Addr().String() {
		t.Fatalf("target mismatch: %+v", got)
	}
}

func TestProbeUnreachable(t *testing.T) {
	// listen to grab a port, then close it, yielding an unreachable target.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	specs := []config.ProbeSpec{{
		Name: "peer", TargetKind: agentproto.ProbeTargetPeer,
		Target: addr, Direction: agentproto.DirectionOut,
		IntervalSec: 5,
	}}
	r := New(specs, log.New(io.Discard, "", 0))
	c := &collect{}
	r.SetSend(c.send)

	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	defer cancel()

	rs := waitReport(t, c)
	got := rs[0]
	if got.Verdict != agentproto.ProbeVerdictSick || got.Reachable {
		t.Fatalf("sick expected: %+v", got)
	}
}

func TestProbeTunnelReturnPath(t *testing.T) {
	// E-28 China-bound return-path probing: tunnel probes from an overseas entry to a domestic landing report as usual,
	// and direction=in is passed through to the panel for return-path reporting.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	specs := []config.ProbeSpec{{
		Name: "cn-landing-rt", TargetKind: agentproto.ProbeTargetTunnel,
		Target: ln.Addr().String(), Direction: agentproto.DirectionIn,
		IntervalSec: 5,
	}}
	r := New(specs, log.New(io.Discard, "", 0))
	c := &collect{}
	r.SetSend(c.send)

	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	defer cancel()

	rs := waitReport(t, c)
	got := rs[0]
	if got.TargetKind != agentproto.ProbeTargetTunnel || got.Direction != agentproto.DirectionIn {
		t.Fatalf("tunnel/in report expected: %+v", got)
	}
	if got.Verdict != agentproto.ProbeVerdictHealthy || !got.Reachable || got.LossPct != 0 {
		t.Fatalf("healthy 0-loss expected: %+v", got)
	}
}

func TestMeasureUnreachable(t *testing.T) {
	// a closed local port: every round fails -> 100 loss, unreachable.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	rtt, jitter, loss, reachable := measureTCP(addr)
	if reachable || loss != 100 || rtt != 0 || jitter != 0 {
		t.Fatalf("want unreachable loss=100, got rtt=%d jitter=%d loss=%d reachable=%v", rtt, jitter, loss, reachable)
	}
}

// TestJitterOf covers F-0907's jitter computation: the mean absolute
// difference between consecutive round RTTs, 0 for stable or single samples.
func TestJitterOf(t *testing.T) {
	cases := []struct {
		name    string
		samples []int
		want    int
	}{
		{"stable", []int{10, 10, 10, 10, 10}, 0},
		{"wobble", []int{10, 20, 10, 20}, 10},
		{"drift down", []int{50, 40, 30}, 10},
		{"single sample", []int{42}, 0},
		{"empty", nil, 0},
		{"two samples", []int{7, 12}, 5},
	}
	for _, tc := range cases {
		if got := jitterOf(tc.samples); got != tc.want {
			t.Fatalf("%s: jitterOf(%v) = %d, want %d", tc.name, tc.samples, got, tc.want)
		}
	}
}

// udpResponder starts a UDP echo server answering every datagram with prefix +
// the payload. A nil prefix echoes the payload unchanged.
func udpResponder(t *testing.T, prefix []byte, drop bool) net.Addr {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("udp listen: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if drop {
				continue
			}
			resp := append(append([]byte{}, prefix...), buf[:n]...)
			if _, err := conn.WriteTo(resp, addr); err != nil {
				return
			}
		}
	}()
	return conn.LocalAddr()
}

// TestMeasureUDPReply covers F-0910's reply rule: a responder that echoes the
// expected prefix is reachable with loss 0.
func TestMeasureUDPReply(t *testing.T) {
	addr := udpResponder(t, []byte{0xde, 0xad}, false)
	spec := config.ProbeSpec{
		Name: "udp-echo", TargetKind: agentproto.ProbeTargetExit, Target: addr.String(),
		Proto: "udp", PayloadHex: "0100", ExpectHex: "dead", IntervalSec: 5,
	}
	rtt, jitter, loss, reachable := measureUDP(spec)
	if !reachable || loss != 0 || rtt < 0 {
		t.Fatalf("want reachable loss=0, got rtt=%d jitter=%d loss=%d reachable=%v", rtt, jitter, loss, reachable)
	}
}

// TestMeasureUDPMismatch covers F-0910's expect_hex rule: a reply that does
// not carry the expected prefix is not evidence of reachability.
func TestMeasureUDPMismatch(t *testing.T) {
	addr := udpResponder(t, []byte{0x00}, false) // echoes a wrong prefix
	spec := config.ProbeSpec{
		Name: "udp-mismatch", TargetKind: agentproto.ProbeTargetExit, Target: addr.String(),
		Proto: "udp", PayloadHex: "aa", ExpectHex: "ff", IntervalSec: 5,
	}
	_, _, loss, reachable := measureUDP(spec)
	if reachable || loss != 100 {
		t.Fatalf("want unreachable loss=100, got loss=%d reachable=%v", loss, reachable)
	}
}

// TestMeasureUDPSilent covers the no-responder case: datagrams that vanish
// without a reply mean unreachable (UDP cannot infer delivery any other way).
func TestMeasureUDPSilent(t *testing.T) {
	addr := udpResponder(t, nil, true)
	spec := config.ProbeSpec{
		Name: "udp-silent", TargetKind: agentproto.ProbeTargetExit, Target: addr.String(),
		Proto: "udp", PayloadHex: "aa", IntervalSec: 5,
	}
	rtt, jitter, loss, reachable := measureUDP(spec)
	if reachable || loss != 100 || rtt != 0 || jitter != 0 {
		t.Fatalf("want unreachable loss=100, got rtt=%d jitter=%d loss=%d reachable=%v", rtt, jitter, loss, reachable)
	}
}

// TestProbeUDPReport covers the runner end: a udp spec produces reports whose
// verdict is healthy against a real responder.
func TestProbeUDPReport(t *testing.T) {
	addr := udpResponder(t, []byte{0xde}, false)
	specs := []config.ProbeSpec{{
		Name: "udp-run", TargetKind: agentproto.ProbeTargetExit, Target: addr.String(),
		Proto: "udp", PayloadHex: "aa", ExpectHex: "de", IntervalSec: 5,
	}}
	r := New(specs, log.New(io.Discard, "", 0))
	c := &collect{}
	r.SetSend(c.send)

	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	defer cancel()

	rs := waitReport(t, c)
	got := rs[0]
	if got.Verdict != agentproto.ProbeVerdictHealthy || !got.Reachable {
		t.Fatalf("healthy expected: %+v", got)
	}
}

func TestVerdictOf(t *testing.T) {
	cases := []struct {
		loss      int
		reachable bool
		want      string
	}{
		{0, true, agentproto.ProbeVerdictHealthy},
		{20, true, agentproto.ProbeVerdictHealthy},
		{50, true, agentproto.ProbeVerdictSick}, // more than half the rounds failing counts as unreachable
		{100, false, agentproto.ProbeVerdictSick},
	}
	for _, c := range cases {
		if got := verdictOf(c.loss, c.reachable); got != c.want {
			t.Fatalf("verdictOf(%d,%v) = %s, want %s", c.loss, c.reachable, got, c.want)
		}
	}
}

func TestNextInterval(t *testing.T) {
	// evening peak = 19:00-23:00 Beijing time (fixed UTC+8, independent of the node's timezone).
	atCN := func(hourUTC int) time.Time {
		return time.Date(2026, 10, 7, hourUTC, 0, 0, 0, time.UTC)
	}
	in := config.ProbeSpec{Direction: agentproto.DirectionIn, IntervalSec: 120}
	out := config.ProbeSpec{Direction: agentproto.DirectionOut, IntervalSec: 120}

	cases := []struct {
		name string
		spec config.ProbeSpec
		now  time.Time
		want time.Duration
	}{
		{"回程晚高峰加密", in, atCN(12), 40 * time.Second},   // 20:00 Beijing time
		{"回程平峰不加密", in, atCN(4), 120 * time.Second},   // 12:00 Beijing time
		{"回程高峰外不加密", in, atCN(15), 120 * time.Second}, // 23:00 Beijing time (right-open window)
		{"出海晚高峰不加密", out, atCN(12), 120 * time.Second},
	}
	for _, c := range cases {
		if got := nextInterval(c.spec, c.now); got != c.want {
			t.Fatalf("%s: nextInterval = %v, want %v", c.name, got, c.want)
		}
	}

	// floor protection: when base is already small, do not regress to a slower interval.
	small := config.ProbeSpec{Direction: agentproto.DirectionIn, IntervalSec: 6}
	if got := nextInterval(small, atCN(12)); got != 6*time.Second {
		t.Fatalf("small base: nextInterval = %v, want 6s", got)
	}
	// raise the floor to 10s, but never slower than base.
	base30 := config.ProbeSpec{Direction: agentproto.DirectionIn, IntervalSec: 30}
	if got := nextInterval(base30, atCN(12)); got != 10*time.Second {
		t.Fatalf("base30: nextInterval = %v, want 10s", got)
	}
}
