// Package quality defines the per-business quality metric sets (§7.3,
// F-0941..F-0944) and the scoring rules that turn probe signals into a
// 0-100 score per business class (F-0945..F-0948). One business, one metric
// set, one rule -- never one universal score dressed up per class: what
// ruins a game (jitter) is not what ruins a video stream (throughput), and
// scoring must respect that.
package quality

import "github.com/cuihairu/ferry/server/internal/storage"

// Business class labels; the metrics each class is judged by, and the report
// window they are read from.
const (
	Game  = "game"
	Web   = "web"
	Video = "video"
	Proxy = "proxy"
)

// Metric is one measurable quality dimension. The name doubles as the
// storage.ProbeReport field (or derivation) the value is read from.
type Metric string

const (
	// Shared transport metrics (already in every ProbeReport).
	MetricRTT    Metric = "rtt_ms"    // mean round-trip of the probe's successful rounds
	MetricJitter Metric = "jitter_ms" // mean |ΔRTT| across rounds (F-0907)
	MetricLoss   Metric = "loss_pct"  // share of the probe's rounds that failed
	MetricAvail  Metric = "avail_pct" // 100 - loss when reachable, 0 when not
	// Web-session metrics (F-0942): where a page-load actually stalls.
	MetricConnect Metric = "connect_ms" // TCP dial time
	MetricTLS     Metric = "tls_ms"     // TLS handshake time after connect
	MetricTTFB    Metric = "ttfb_ms"    // request sent to first response byte
	// Video-stream metrics (F-0943): what makes playback stall.
	MetricThroughput Metric = "throughput_kbps" // sustained body download rate
	MetricBufferRisk Metric = "buffer_risk"     // stall likelihood proxy: loss + jitter headroom
)

// MetricInfo is one metric of a set: where it comes from so a report builder
// never guesses the mapping.
type MetricInfo struct {
	Metric Metric
	// Derive reads the metric off one probe report; nil for metrics that
	// need a window (e.g. fail rate over N reports), which read from the
	// aggregate instead.
	Derive func(storage.ProbeReport) int
}

// MetricSet is one business class's ordered metric list, decisive first.
type MetricSet struct {
	Business string
	Metrics  []MetricInfo
}

// gameSet is the F-0941 game quality metric set. Games die on latency and
// rhythm, not bandwidth: round-trip, jitter, loss and the resulting
// availability are the four numbers a player feels, and all four are already
// in every ProbeReport -- no new probe plumbing needed.
func gameSet() MetricSet {
	return MetricSet{Business: Game, Metrics: []MetricInfo{
		{Metric: MetricRTT, Derive: func(r storage.ProbeReport) int { return r.RttMs }},
		{Metric: MetricJitter, Derive: func(r storage.ProbeReport) int { return r.JitterMs }},
		{Metric: MetricLoss, Derive: func(r storage.ProbeReport) int { return r.LossPct }},
		{Metric: MetricAvail, Derive: func(r storage.ProbeReport) int { return availPct(r) }},
	}}
}

// webSet is the F-0942 web quality metric set: a page load lives or dies on
// the session phases -- TCP connect, TLS handshake, time to first byte --
// plus the round failure rate. All four are measured by http/https probes
// since the F-0942 phase tracing; plaintext probes simply carry tls 0.
func webSet() MetricSet {
	return MetricSet{Business: Web, Metrics: []MetricInfo{
		{Metric: MetricConnect, Derive: func(r storage.ProbeReport) int { return r.ConnectMs }},
		{Metric: MetricTLS, Derive: func(r storage.ProbeReport) int { return r.TLSMs }},
		{Metric: MetricTTFB, Derive: func(r storage.ProbeReport) int { return r.TTFBMs }},
		{Metric: MetricLoss, Derive: func(r storage.ProbeReport) int { return r.LossPct }},
	}}
}

// videoSet is the F-0943 video quality metric set: playback stalls when the
// sustained throughput cannot keep the bitrate, when packets drop, or when
// arrival rhythm wobbles. Throughput needs the probe's download_kb opt-in
// (0 = not measured, and scoring must treat it as no datum, not as zero).
func videoSet() MetricSet {
	return MetricSet{Business: Video, Metrics: []MetricInfo{
		{Metric: MetricThroughput, Derive: func(r storage.ProbeReport) int { return r.ThroughputKbps }},
		{Metric: MetricLoss, Derive: func(r storage.ProbeReport) int { return r.LossPct }},
		{Metric: MetricJitter, Derive: func(r storage.ProbeReport) int { return r.JitterMs }},
		{Metric: MetricBufferRisk, Derive: bufferRisk},
	}}
}

// proxySet is the F-0944 generic-proxy quality metric set: without an
// application shape to model, the proxy experience is the transport itself --
// latency, loss and the resulting availability.
func proxySet() MetricSet {
	return MetricSet{Business: Proxy, Metrics: []MetricInfo{
		{Metric: MetricRTT, Derive: func(r storage.ProbeReport) int { return r.RttMs }},
		{Metric: MetricLoss, Derive: func(r storage.ProbeReport) int { return r.LossPct }},
		{Metric: MetricAvail, Derive: func(r storage.ProbeReport) int { return availPct(r) }},
	}}
}

// bufferRisk is the F-0943 stall-likelihood proxy in 0-100, derived from the
// transport signals a video stream feels: each lost round is worth double (a
// lost round is a stall, not just noise), and jitter adds ms-for-points
// because arrival gaps drain the playback buffer no matter how low the
// average latency. It is a proxy, not a playback simulation -- the honest
// label is risk, not prediction.
func bufferRisk(r storage.ProbeReport) int {
	if !r.Reachable {
		return 100
	}
	risk := r.LossPct*2 + r.JitterMs
	if risk > 100 {
		risk = 100
	}
	return risk
}

// Set returns the metric set for a business class (F-0941..F-0944). An
// unknown class returns no metrics -- callers surface the empty set rather
// than falling back to some default set that would silently mislabel the
// business.
func Set(business string) MetricSet {
	switch business {
	case Game:
		return gameSet()
	case Web:
		return webSet()
	case Video:
		return videoSet()
	case Proxy:
		return proxySet()
	default:
		return MetricSet{Business: business}
	}
}

// availPct is availability from one report: reachable with all rounds
// answering is 100, unreachable is 0 (absence of evidence is not
// availability).
func availPct(r storage.ProbeReport) int {
	if !r.Reachable {
		return 0
	}
	return 100 - r.LossPct
}
