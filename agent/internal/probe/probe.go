// Package probe does edge probing: it periodically measures target reachability, latency and packet loss per spec
// (several dial rounds, over half failing marks it sick); the direction=in return-path probing intensifies during evening peak.
// Probes run locally on the node and their conclusions are reported to the panel via probe.report;
// the panel only collects conclusions for aggregate judgement and does no centralized probing.
package probe

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// Probing semantics: one probe dials probeRounds times (packet loss = share of failed rounds),
// pausing roundSpacing between rounds; more than half the rounds failing counts as unreachable.
const (
	dialTimeout  = 3 * time.Second
	probeRounds  = 5
	roundSpacing = 200 * time.Millisecond
	sickLossPct  = 50

	// Evening peak window and intensification factor (19:00-23:00 Beijing time, fixed UTC+8 regardless of the node's timezone):
	// the China-bound experience lives entirely in the evening peak; daytime data is not representative (design doc §2.3).
	peakHourStart = 19
	peakHourEnd   = 23
	peakDivisor   = 3
	minPeakGap    = 10 * time.Second
)

// Runner executes edge probes periodically per spec.
type Runner struct {
	log   *log.Logger
	specs []config.ProbeSpec

	sendMu sync.Mutex
	send   func(agentproto.Envelope) error // sender of the current connection
}

// New creates a probe runner.
func New(specs []config.ProbeSpec, logger *log.Logger) *Runner {
	if logger == nil {
		logger = log.Default()
	}
	return &Runner{log: logger, specs: specs}
}

// SetSend sets the report sender; app updates it after the agent reconnects.
func (r *Runner) SetSend(send func(agentproto.Envelope) error) {
	r.sendMu.Lock()
	r.send = send
	r.sendMu.Unlock()
}

// Run blocks until the ctx is cancelled: one probe loop per spec.
// Tunnel probing (E-28 China-bound return path) dials the spec's target port directly -- the same network path
// the relay uses to reach the landing, but independent of the relay process (a forwarding crash does not take probing down).
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, spec := range r.specs {
		wg.Add(1)
		go func(spec config.ProbeSpec) {
			defer wg.Done()
			r.loop(ctx, spec)
		}(spec)
	}
	wg.Wait()
}

// loop is the periodic probe loop for one spec; the next interval follows the evening-peak rule.
func (r *Runner) loop(ctx context.Context, spec config.ProbeSpec) {
	t := time.NewTimer(0) // probe once at startup
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.probeOnce(spec)
		t.Reset(nextInterval(spec, time.Now()))
	}
}

// probeOnce runs one multi-round probe and reports the conclusion; dropped silently when offline.
func (r *Runner) probeOnce(spec config.ProbeSpec) {
	rtt, jitter, loss, reachable := measure(spec.Target)
	// region/ISP snapshots are filled in by the panel when it stores the target node.
	report := agentproto.ProbeReport{
		TargetKind: spec.TargetKind,
		TargetHost: spec.Target,
		Direction:  spec.Direction,
		RttMs:      rtt,
		JitterMs:   jitter,
		LossPct:    loss,
		Reachable:  reachable,
		Verdict:    verdictOf(loss, reachable),
		ProbedAt:   time.Now(),
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("probe-%d", time.Now().UnixNano()),
		agentproto.MsgProbeReport,
		agentproto.ProbeReportBatch{Items: []agentproto.ProbeReport{report}},
	)
	if err != nil {
		return
	}
	r.sendMu.Lock()
	send := r.send
	r.sendMu.Unlock()
	if send == nil {
		return
	}
	if err := send(env); err != nil {
		r.log.Printf("probe %s report: %v", spec.Name, err)
	}
}

// measure dials the target probeRounds times over TCP: it returns the average successful RTT (ms),
// the RTT jitter across this probe's successful rounds (F-0907), the packet loss rate 0-100 and
// reachability (any successful round means reachable).
func measure(target string) (rttMs, jitterMs, lossPct int, reachable bool) {
	var samples []int
	for i := 0; i < probeRounds; i++ {
		if i > 0 {
			time.Sleep(roundSpacing)
		}
		start := time.Now()
		conn, err := net.DialTimeout("tcp", target, dialTimeout)
		if err != nil {
			continue
		}
		_ = conn.Close()
		samples = append(samples, int(time.Since(start).Milliseconds()))
	}
	if len(samples) == 0 {
		return 0, 0, 100, false
	}
	sum := 0
	for _, s := range samples {
		sum += s
	}
	return sum / len(samples), jitterOf(samples), (probeRounds - len(samples)) * 100 / probeRounds, true
}

// jitterOf is the mean absolute difference between consecutive successful round
// RTTs (F-0907): 0 for a perfectly stable path, larger the more the latency
// wobbles within one probe; fewer than two samples means no jitter datum (0).
func jitterOf(samples []int) int {
	if len(samples) < 2 {
		return 0
	}
	sum := 0
	for i := 1; i < len(samples); i++ {
		d := samples[i] - samples[i-1]
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return sum / (len(samples) - 1)
}

// verdictOf decides by reachability and loss: unreachable or over half the rounds failing means sick --
// during evening peak, over half lost means a degraded return path, so aggregation must be able to pick sick ones out.
func verdictOf(lossPct int, reachable bool) string {
	if !reachable || lossPct >= sickLossPct {
		return agentproto.ProbeVerdictSick
	}
	return agentproto.ProbeVerdictHealthy
}

// nextInterval returns the next probe interval: direction=in return-path probing during evening peak
// (19:00-23:00 Beijing time) intensifies to base/3 (floor 10s, and never slower than base).
func nextInterval(spec config.ProbeSpec, now time.Time) time.Duration {
	interval := time.Duration(spec.IntervalSec) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if spec.Direction != agentproto.DirectionIn || !inPeak(now) {
		return interval
	}
	peak := interval / peakDivisor
	if peak < minPeakGap {
		peak = minPeakGap
	}
	if peak > interval {
		peak = interval // base is already below the floor, do not regress to something slower
	}
	return peak
}

// inPeak decides whether the time falls in the evening-peak window: Beijing time (fixed UTC+8).
func inPeak(now time.Time) bool {
	h := (now.UTC().Hour() + 8) % 24
	return h >= peakHourStart && h < peakHourEnd
}
