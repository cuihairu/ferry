package billing

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Billing sync metrics (F-1813): every import run settles exactly once
// through its finish closure, so the counter counts outcomes by source
// (success rate = succeeded / total at query time) and the histogram the run
// wall time (finish minus start). partial keeps its own result: rows landed
// but the run flagged an error.
var (
	syncTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ferry_billing_sync_total",
		Help: "Bill import runs by source and result (succeeded/failed/partial).",
	}, []string{"source", "result"})
	syncSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ferry_billing_sync_seconds",
		Help:    "Bill import run wall time by source (api/file).",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"source"})
)

// recordSync observes one settled import run.
func recordSync(source, state string, d time.Duration) {
	syncTotal.WithLabelValues(source, state).Inc()
	syncSeconds.WithLabelValues(source).Observe(d.Seconds())
}
