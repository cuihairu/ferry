// Package probe does edge probing: it periodically measures target reachability, latency and packet loss per spec
// (several dial rounds, over half failing marks it sick); the direction=in return-path probing intensifies during evening peak.
// Probes run locally on the node and their conclusions are reported to the panel via probe.report;
// the panel only collects conclusions for aggregate judgement and does no centralized probing.
package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"syscall"
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

	// defaultProbeConcurrency caps how many probes measure at the same time
	// (F-0922): a large dispatched set must not open dozens of sockets at once.
	defaultProbeConcurrency = 16

	// Fault speed-up (F-0925): after a sick verdict the spec temporarily probes
	// sickBoostDivisor times faster for sickBoostWindow, so recovery is noticed
	// in minutes instead of full intervals. The cadence never drops below
	// minBoostGap, and never above the spec's own interval.
	sickBoostWindow  = 5 * time.Minute
	sickBoostDivisor = 4
	minBoostGap      = 10 * time.Second

	// Anti-storm phase spread (F-0926): when many agents run peer probes, all
	// of them starting loops on the same tick would fire a thundering herd of
	// cross-probes at the targets. Each loop waits a random slice of this
	// window (never more than its own interval) before its first probe, so
	// fleets desynchronize. 0 disables the spread.
	stormJitterWindow = 45 * time.Second
)

// Runner executes edge probes periodically per spec.
type Runner struct {
	log *log.Logger

	mu           sync.Mutex
	specs        []config.ProbeSpec
	root         context.Context    // Run's context; nil until Run starts
	cancel       context.CancelFunc // cancels the currently running loop set
	wg           sync.WaitGroup
	sem          chan struct{} // measure permits (F-0922)
	jitterWindow time.Duration // F-0926 anti-storm first-probe spread; 0 = off

	sendMu sync.Mutex
	send   func(agentproto.Envelope) error // sender of the current connection
}

// New creates a probe runner.
func New(specs []config.ProbeSpec, logger *log.Logger) *Runner {
	if logger == nil {
		logger = log.Default()
	}
	return &Runner{log: logger, specs: specs, sem: make(chan struct{}, defaultProbeConcurrency),
		jitterWindow: stormJitterWindow}
}

// SetStartJitter overrides the F-0926 anti-storm spread before Run; 0 makes
// every loop fire its first probe immediately (tests, single-agent setups).
func (r *Runner) SetStartJitter(d time.Duration) {
	r.mu.Lock()
	r.jitterWindow = d
	r.mu.Unlock()
}

// startDelay is the random pre-first-probe delay for one loop (F-0926): a
// random slice of the jitter window, never more than the spec's own interval,
// so a large interval spec still probes promptly.
func (r *Runner) startDelay(spec config.ProbeSpec) time.Duration {
	r.mu.Lock()
	window := r.jitterWindow
	r.mu.Unlock()
	if window <= 0 {
		return 0
	}
	if iv := time.Duration(spec.IntervalSec) * time.Second; iv > 0 && iv < window {
		window = iv
	}
	return time.Duration(rand.Int63n(int64(window) + 1))
}

// SetConcurrency caps simultaneous probes (F-0922); 0 falls back to the
// default. Call it before Run: swapping the permit channel while loops run
// would strand permits in the old channel.
func (r *Runner) SetConcurrency(n int) {
	if n <= 0 {
		n = defaultProbeConcurrency
	}
	r.mu.Lock()
	r.sem = make(chan struct{}, n)
	r.mu.Unlock()
}

// Concurrency returns the current simultaneous-probe cap (F-0922).
func (r *Runner) Concurrency() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cap(r.sem)
}

// acquire takes a measure permit (F-0922) and returns the release func; the
// permit spans the whole multi-round measure, not the report.
func (r *Runner) acquire() func() {
	r.mu.Lock()
	sem := r.sem
	r.mu.Unlock()
	sem <- struct{}{}
	return func() { <-sem }
}

// SetSend sets the report sender; app updates it after the agent reconnects.
func (r *Runner) SetSend(send func(agentproto.Envelope) error) {
	r.sendMu.Lock()
	r.send = send
	r.sendMu.Unlock()
}

// Specs returns the current spec set (dispatch bookkeeping and tests).
func (r *Runner) Specs() []config.ProbeSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.specs
}

// Replace swaps the spec set (F-0921): the running loops stop and the new set
// starts probing immediately. Called before Run it only swaps the set that Run
// will start with. Each loop probes once at startup, so a dispatched change
// produces fresh verdicts right away.
func (r *Runner) Replace(specs []config.ProbeSpec) {
	r.mu.Lock()
	r.specs = specs
	root := r.root
	r.mu.Unlock()
	if root == nil {
		return // Run not started yet: the new set takes effect at startup
	}
	r.stopLoops()
	r.startLoops()
}

// Run blocks until the ctx is cancelled: one probe loop per spec.
// Tunnel probing (E-28 China-bound return path) dials the spec's target port directly -- the same network path
// the relay uses to reach the landing, but independent of the relay process (a forwarding crash does not take probing down).
func (r *Runner) Run(ctx context.Context) {
	r.mu.Lock()
	r.root = ctx
	r.mu.Unlock()
	r.startLoops()
	<-ctx.Done()
	r.stopLoops()
}

// startLoops launches one loop per current spec; never called with mu held.
func (r *Runner) startLoops() {
	r.mu.Lock()
	if r.root == nil || r.cancel != nil {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(r.root)
	r.cancel = cancel
	specs := r.specs
	r.wg.Add(len(specs))
	r.mu.Unlock()
	for _, spec := range specs {
		go func(spec config.ProbeSpec) {
			defer r.wg.Done()
			r.loop(ctx, spec)
		}(spec)
	}
}

// stopLoops cancels the current loop set and waits for it to finish.
func (r *Runner) stopLoops() {
	r.mu.Lock()
	cancel := r.cancel
	r.cancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
}

// loop is the periodic probe loop for one spec; the next interval follows the
// evening-peak rule plus the F-0925 fault speed-up after a sick verdict.
func (r *Runner) loop(ctx context.Context, spec config.ProbeSpec) {
	// F-0926: spread the first probe across the jitter window, so Replace-driven
	// dispatch herds and fleet-wide loop starts stay desynchronized.
	t := time.NewTimer(r.startDelay(spec))
	defer t.Stop()
	var boostUntil time.Time // F-0925: sick verdicts speed this spec up for a while
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if sick := r.probeOnce(spec); sick {
			boostUntil = time.Now().Add(sickBoostWindow)
		}
		t.Reset(nextIntervalBoosted(spec, time.Now(), boostUntil))
	}
}

// nextIntervalBoosted is nextInterval with the F-0925 fault speed-up: inside
// the boost window after a sick verdict the spec probes faster so recovery is
// noticed in minutes. The boosted gap is floored at minBoostGap and capped at
// the plain interval, so the speed-up can never backfire into a longer wait.
func nextIntervalBoosted(spec config.ProbeSpec, now time.Time, boostUntil time.Time) time.Duration {
	base := nextInterval(spec, now)
	if !now.Before(boostUntil) {
		return base
	}
	d := base / sickBoostDivisor
	if d < minBoostGap {
		d = minBoostGap
	}
	if d > base {
		d = base
	}
	return d
}

// probeOnce runs one multi-round probe and reports the conclusion; dropped
// silently when offline. Returns whether the verdict is sick (F-0925).
func (r *Runner) probeOnce(spec config.ProbeSpec) bool {
	release := r.acquire() // F-0922: simultaneous measures are capped
	rtt, jitter, loss, reachable, errClass, phases := r.measure(spec)
	release()
	verdict := verdictOf(loss, reachable)
	// region/ISP snapshots are filled in by the panel when it stores the target node.
	report := agentproto.ProbeReport{
		TargetKind: spec.TargetKind,
		TargetHost: spec.Target,
		Direction:  spec.Direction,
		RttMs:      rtt,
		JitterMs:   jitter,
		LossPct:    loss,
		Reachable:  reachable,
		ErrClass:   errClass,
		Verdict:    verdict,
		ProbedAt:   time.Now(),
		// Web-session phases (F-0942/F-0943): zero outside http/https.
		ConnectMs:      phases.connectMs,
		TLSMs:          phases.tlsMs,
		TTFBMs:         phases.ttfbMs,
		ThroughputKbps: phases.throughputKbps,
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("probe-%d", time.Now().UnixNano()),
		agentproto.MsgProbeReport,
		agentproto.ProbeReportBatch{Items: []agentproto.ProbeReport{report}},
	)
	if err != nil {
		return false
	}
	r.sendMu.Lock()
	send := r.send
	r.sendMu.Unlock()
	if send == nil {
		return false
	}
	if err := send(env); err != nil {
		r.log.Printf("probe %s report: %v", spec.Name, err)
	}
	return verdict == agentproto.ProbeVerdictSick
}

// measure dispatches per spec: TCP connect probing (default), UDP request/
// reply probing (F-0910), or HTTP(S) application-layer probing (F-0911). It
// returns the average successful RTT (ms), the RTT jitter across this probe's
// successful rounds (F-0907), the packet loss rate 0-100, reachability, the
// failure class of the failed rounds (F-0913; empty when any round won) and
// the web-session phases (F-0942, zero outside http/https).
func (r *Runner) measure(spec config.ProbeSpec) (rttMs, jitterMs, lossPct int, reachable bool, errClass string, phases httpPhases) {
	switch spec.Proto {
	case "udp":
		rtt, jitter, loss, ok, class := measureUDP(spec)
		return rtt, jitter, loss, ok, class, httpPhases{}
	case "http", "https":
		return measureHTTP(spec)
	}
	rtt, jitter, loss, ok, class := measureTCP(spec)
	return rtt, jitter, loss, ok, class, httpPhases{}
}

// effTimeout is the spec's per-round timeout (F-0913), falling back to the
// shared default when unset.
func effTimeout(spec config.ProbeSpec) time.Duration {
	if spec.TimeoutSec > 0 {
		return time.Duration(spec.TimeoutSec) * time.Second
	}
	return dialTimeout
}

// measureTCP dials the target probeRounds times over TCP: any successful
// connect means reachable.
func measureTCP(spec config.ProbeSpec) (rttMs, jitterMs, lossPct int, reachable bool, errClass string) {
	timeout := effTimeout(spec)
	var samples []int
	var classes []string
	for i := 0; i < probeRounds; i++ {
		if i > 0 {
			time.Sleep(roundSpacing)
		}
		start := time.Now()
		conn, err := net.DialTimeout("tcp", spec.Target, timeout)
		if err != nil {
			classes = append(classes, classifyNetErr(err))
			continue
		}
		_ = conn.Close()
		samples = append(samples, int(time.Since(start).Milliseconds()))
	}
	return summarize(samples, classes)
}

// measureUDP sends the spec's payload probeRounds times and waits for a reply
// (F-0910): UDP is connectionless, so a round only counts when something
// answers; with expect_hex set the reply must carry that byte prefix, which
// is how e.g. a DNS baseline distinguishes a real responder from random
// backscatter. RTT runs write -> first matching read.
func measureUDP(spec config.ProbeSpec) (rttMs, jitterMs, lossPct int, reachable bool, errClass string) {
	payload, err := hex.DecodeString(spec.PayloadHex)
	if err != nil {
		return 0, 0, 100, false, "other" // validated at config load; unreachable beats lying
	}
	expect, _ := hex.DecodeString(spec.ExpectHex)
	timeout := effTimeout(spec)

	var samples []int
	var classes []string
	for i := 0; i < probeRounds; i++ {
		if i > 0 {
			time.Sleep(roundSpacing)
		}
		conn, err := net.DialTimeout("udp", spec.Target, timeout)
		if err != nil {
			classes = append(classes, classifyNetErr(err))
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(timeout))
		start := time.Now()
		if _, err := conn.Write(payload); err != nil {
			classes = append(classes, classifyNetErr(err))
			_ = conn.Close()
			continue
		}
		buf := make([]byte, 2048)
		n, err := conn.Read(buf)
		_ = conn.Close()
		if err != nil || n == 0 {
			classes = append(classes, "no_reply")
			continue
		}
		if len(expect) > 0 && !bytes.HasPrefix(buf[:n], expect) {
			classes = append(classes, "mismatch")
			continue
		}
		samples = append(samples, int(time.Since(start).Milliseconds()))
	}
	return summarize(samples, classes)
}

// summarize turns the successful round samples into the report tuple: average
// RTT, jitter across consecutive samples (F-0907), loss percentage,
// reachability (any successful round means reachable) and -- only when every
// round failed -- the majority failure class (F-0913).
func summarize(samples []int, classes []string) (rttMs, jitterMs, lossPct int, reachable bool, errClass string) {
	if len(samples) == 0 {
		return 0, 0, 100, false, majorityClass(classes)
	}
	sum := 0
	for _, s := range samples {
		sum += s
	}
	return sum / len(samples), jitterOf(samples), (probeRounds - len(samples)) * 100 / probeRounds, true, ""
}

// majorityClass returns the most frequent failure class of the failed rounds
// (F-0913); "other" as a fallback for a classless failure.
func majorityClass(classes []string) string {
	if len(classes) == 0 {
		return "other"
	}
	count := map[string]int{}
	best, bestN := "", 0
	for _, c := range classes {
		count[c]++
		if count[c] > bestN {
			best, bestN = c, count[c]
		}
	}
	if best == "" {
		return "other"
	}
	return best
}

// classifyNetErr maps a transport error to the F-0913 failure vocabulary:
// dns / timeout / refused / reset / tls win over the generic "other", matched
// from the most specific predicate outwards. In the UDP path a read-deadline
// expiry is reported as no_reply instead (someone must answer; silence is
// the observation), so the timeout class here is dominated by dials.
func classifyNetErr(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "refused"
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return "reset"
	}
	if errors.Is(err, tls.RecordHeaderError{}) || errors.Is(err, x509.UnknownAuthorityError{}) ||
		errors.Is(err, x509.CertificateInvalidError{}) || errors.Is(err, x509.HostnameError{}) {
		return "tls"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" && strings.Contains(strings.ToLower(err.Error()), "tls") {
		return "tls"
	}
	return "other"
}

// httpPhases carries the mean web-session phase timings of one probe's
// successful rounds (F-0942): where a page load actually stalls. tlsMs is 0
// for plaintext probes, throughputKbps only when the spec opts into a body
// download (F-0943).
type httpPhases struct {
	connectMs      int
	tlsMs          int
	ttfbMs         int
	throughputKbps int
}

// measureHTTP issues a HEAD request per round (F-0911) -- or GET with a
// capped body read when download_kb opts in (F-0943): a round succeeds when
// the service answers with an HTTP response -- redirects count as answered,
// they are not followed -- and with expect_status set, only that exact status
// counts. RTT runs request start to response headers; the traced phases
// (connect / TLS / TTFB, F-0942) and the download rate (F-0943) average over
// the successful rounds too.
func measureHTTP(spec config.ProbeSpec) (rttMs, jitterMs, lossPct int, reachable bool, errClass string, phases httpPhases) {
	path := spec.HTTPPath
	if path == "" {
		path = "/"
	}
	// HEAD, not GET: the question is "does the application answer", not "how
	// fast can it serve bodies"; redirects are surfaced, never followed. The
	// download_kb opt-in (F-0943) deliberately pays for a body to measure.
	method := http.MethodHead
	if spec.DownloadKB > 0 {
		method = http.MethodGet
	}
	client := &http.Client{
		Timeout: effTimeout(spec),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	var samples []int
	var classes []string
	var connectSamples, tlsSamples, ttfbSamples, rateSamples []int
	for i := 0; i < probeRounds; i++ {
		if i > 0 {
			time.Sleep(roundSpacing)
		}
		req, err := http.NewRequest(method, "http://"+spec.Target+path, nil)
		if err != nil {
			classes = append(classes, "other")
			continue
		}
		if spec.Proto == "https" {
			req.URL.Scheme = "https"
		}
		var (
			connectAt, tlsAt time.Time
			connect, tlsDur  time.Duration
			ttfb             time.Duration
		)
		reqStart := time.Now()
		trace := &httptrace.ClientTrace{
			ConnectStart: func(_, _ string) { connectAt = time.Now() },
			ConnectDone: func(_, _ string, err error) {
				if err == nil && !connectAt.IsZero() {
					connect = time.Since(connectAt)
				}
			},
			TLSHandshakeStart: func() { tlsAt = time.Now() },
			TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
				if err == nil && !tlsAt.IsZero() {
					tlsDur = time.Since(tlsAt)
				}
			},
			GotFirstResponseByte: func() { ttfb = time.Since(reqStart) },
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
		resp, err := client.Do(req)
		if err != nil {
			classes = append(classes, classifyNetErr(err))
			continue
		}
		if spec.DownloadKB > 0 {
			start := time.Now()
			n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, int64(spec.DownloadKB)*1024))
			if n > 0 && resp.StatusCode/100 == 2 {
				if rate := kbps(n, time.Since(start)); rate > 0 {
					rateSamples = append(rateSamples, rate)
				}
			}
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
		}
		_ = resp.Body.Close()
		if spec.ExpectStatus > 0 && resp.StatusCode != spec.ExpectStatus {
			classes = append(classes, "http_status")
			continue
		}
		samples = append(samples, int(time.Since(reqStart).Milliseconds()))
		if connect > 0 {
			connectSamples = append(connectSamples, int(connect.Milliseconds()))
		}
		if tlsDur > 0 {
			tlsSamples = append(tlsSamples, int(tlsDur.Milliseconds()))
		}
		if ttfb > 0 {
			ttfbSamples = append(ttfbSamples, int(ttfb.Milliseconds()))
		}
	}
	phases = httpPhases{
		connectMs:      meanOf(connectSamples),
		tlsMs:          meanOf(tlsSamples),
		ttfbMs:         meanOf(ttfbSamples),
		throughputKbps: meanOf(rateSamples),
	}
	rtt, jitter, loss, ok, class := summarize(samples, classes)
	return rtt, jitter, loss, ok, class, phases
}

// meanOf averages rounded to whole units; an empty list means no datum (0),
// matching the report fields' omit-when-unknown convention.
func meanOf(samples []int) int {
	if len(samples) == 0 {
		return 0
	}
	sum := 0
	for _, s := range samples {
		sum += s
	}
	return sum / len(samples)
}

// kbps converts one body read into kilobits per second.
func kbps(n int64, d time.Duration) int {
	if n <= 0 || d <= 0 {
		return 0
	}
	return int(float64(n) * 8 / d.Seconds() / 1000)
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
