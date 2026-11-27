package cloudprov

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Provider API call metrics (F-1811): success rate = ok / total per op from
// the counter; latency is observed per op only for calls that passed the
// compat and rate-limit gates, covering the full bounded retry loop (attempts
// plus backoff). The op label is the bounded Op* constant set.
var (
	providerCallTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ferry_provider_api_total",
		Help: "Provider API operations by op and result (ok/error/rate_limited/compat).",
	}, []string{"op", "result"})
	providerCallSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ferry_provider_api_seconds",
		Help:    "Provider API operation wall time by op (gate-passed calls, incl. retries).",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"op"})
)

// recordGate counts a call refused before any vendor attempt (compat floor or
// client-side rate limit): no latency observation, none happened.
func recordGate(op, result string) {
	providerCallTotal.WithLabelValues(op, result).Inc()
}

// recordCall counts the outcome and observes the attempt-loop wall time.
func recordCall(op, result string, d time.Duration) {
	providerCallTotal.WithLabelValues(op, result).Inc()
	providerCallSeconds.WithLabelValues(op).Observe(d.Seconds())
}
