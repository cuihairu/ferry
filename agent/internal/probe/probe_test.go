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

// collect 收集上报结论的发送口。
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

// waitReport 等待首条结论上报。
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
	// 先监听拿端口再关闭，得到一个不可达目标。
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

func TestProbeSkipsTunnel(t *testing.T) {
	// 隧道探测在 relay 接入前跳过：不产生任何上报。
	specs := []config.ProbeSpec{{
		Name: "t", TargetKind: agentproto.ProbeTargetTunnel,
		Target: "10.0.0.1:443", IntervalSec: 5,
	}}
	r := New(specs, log.New(io.Discard, "", 0))
	c := &collect{}
	r.SetSend(c.send)

	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()

	if rs := c.all(); len(rs) != 0 {
		t.Fatalf("tunnel probe must be skipped, got %+v", rs)
	}
}
