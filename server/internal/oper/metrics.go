package oper

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Cloud operation task metrics (F-1812): the outcome counter fires at every
// state-changing settlement (success rate = succeeded / total at query time;
// unknown is counted at settlement and again as its reconciled verdict, so
// the two are not disjoint); the duration histogram covers executed paths as
// settlement-minus-creation wall time. The op label is the bounded Op* set.
var (
	opOutcome = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ferry_cloud_op_total",
		Help: "Cloud operation tasks by op and settled result (succeeded/failed/unknown/cancelled).",
	}, []string{"op", "result"})
	opDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ferry_cloud_op_seconds",
		Help:    "Cloud operation task wall time by op (settlement minus creation).",
		Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 300},
	}, []string{"op"})
)

// recordOutcome counts one settlement without a duration (cancel path: the
// row is loaded for the guard but the queue wait is not meaningful there).
func recordOutcome(op, result string) {
	opOutcome.WithLabelValues(op, result).Inc()
}

// recordOutcomeTimed counts one settlement and observes the task's wall time.
func recordOutcomeTimed(op, result string, d time.Duration) {
	recordOutcome(op, result)
	opDuration.WithLabelValues(op).Observe(d.Seconds())
}
