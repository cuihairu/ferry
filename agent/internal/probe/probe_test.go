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

func TestProbeTunnelReturnPath(t *testing.T) {
	// E-28 回国回程探测：海外入口对国内落地的 tunnel 探测照常上报，
	// direction=in 透传给面板做回程报表。
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
	// 关闭的本地端口：全轮失败 → 丢包 100、不可达。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	rtt, loss, reachable := measure(addr)
	if reachable || loss != 100 || rtt != 0 {
		t.Fatalf("want unreachable loss=100, got rtt=%d loss=%d reachable=%v", rtt, loss, reachable)
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
		{50, true, agentproto.ProbeVerdictSick}, // 过半轮失败按不可达处置
		{100, false, agentproto.ProbeVerdictSick},
	}
	for _, c := range cases {
		if got := verdictOf(c.loss, c.reachable); got != c.want {
			t.Fatalf("verdictOf(%d,%v) = %s, want %s", c.loss, c.reachable, got, c.want)
		}
	}
}

func TestNextInterval(t *testing.T) {
	// 晚高峰 = 北京时间 19–23 时（固定 UTC+8，与节点时区无关）。
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
		{"回程晚高峰加密", in, atCN(12), 40 * time.Second},   // 北京 20 时
		{"回程平峰不加密", in, atCN(4), 120 * time.Second},   // 北京 12 时
		{"回程高峰外不加密", in, atCN(15), 120 * time.Second}, // 北京 23 时（窗口右开）
		{"出海晚高峰不加密", out, atCN(12), 120 * time.Second},
	}
	for _, c := range cases {
		if got := nextInterval(c.spec, c.now); got != c.want {
			t.Fatalf("%s: nextInterval = %v, want %v", c.name, got, c.want)
		}
	}

	// 下限保护：base 已很小时不倒退成更慢的间隔。
	small := config.ProbeSpec{Direction: agentproto.DirectionIn, IntervalSec: 6}
	if got := nextInterval(small, atCN(12)); got != 6*time.Second {
		t.Fatalf("small base: nextInterval = %v, want 6s", got)
	}
	// 下限抬到 10s，但绝不慢于 base。
	base30 := config.ProbeSpec{Direction: agentproto.DirectionIn, IntervalSec: 30}
	if got := nextInterval(base30, atCN(12)); got != 10*time.Second {
		t.Fatalf("base30: nextInterval = %v, want 10s", got)
	}
}
