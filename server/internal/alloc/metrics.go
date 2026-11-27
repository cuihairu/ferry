package alloc

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Scheduling cost metrics (F-1810): one histogram observation per scheduler
// round, covering the decision pipeline only (allocation sweep + failover
// rebinds). Announce webhooks and the switch hooks (relay re-pointing over
// config.push) are delivery, not scheduling — their latency is excluded so a
// slow webhook cannot mask the scheduler's own cost.
var (
	sweepSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "ferry_alloc_sweep_seconds",
		Help:    "Duration of one alloc scheduler round (decision pipeline only).",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
	})
	sweepTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ferry_alloc_sweep_total",
		Help: "Alloc scheduler rounds by result (ok/error).",
	}, []string{"result"})
)

// recordSweep observes one round's decision-pipeline duration and outcome.
func recordSweep(d time.Duration, err error) {
	sweepSeconds.Observe(d.Seconds())
	if err != nil {
		sweepTotal.WithLabelValues("error").Inc()
		return
	}
	sweepTotal.WithLabelValues("ok").Inc()
}
