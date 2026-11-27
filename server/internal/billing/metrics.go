package billing

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gorm.io/gorm"
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

// ---- Cost data freshness (F-1814): scrape-time age per cost source, from
// the source-level LastSyncAt rollup. A source never synced has no freshness
// yet, so its series is absent rather than fake-zero. ----

var costSourceAgeDesc = prometheus.NewDesc("ferry_cost_source_age_seconds",
	"Age of the newest bill sync per cost source (now minus last_sync_at).",
	[]string{"source_id"}, nil)

var (
	freshnessOnce sync.Once
	freshnessDB   atomic.Pointer[gorm.DB]
)

type costFreshnessCollector struct{}

func (costFreshnessCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- costSourceAgeDesc
}

func (costFreshnessCollector) Collect(ch chan<- prometheus.Metric) {
	db := freshnessDB.Load()
	if db == nil {
		return
	}
	var rows []storage.CostProvider
	if err := db.Select("id, last_sync_at").
		Where("last_sync_at IS NOT NULL").Find(&rows).Error; err != nil {
		return // scrape read failure: series absent, never panic
	}
	now := time.Now()
	for _, r := range rows {
		ch <- prometheus.MustNewConstMetric(costSourceAgeDesc, prometheus.GaugeValue,
			now.Sub(*r.LastSyncAt).Seconds(), strconv.FormatUint(uint64(r.ID), 10))
	}
}

// instrumentCostFreshness wires the service DB into the freshness collector
// (sync.Once keeps test constructions from double-registering).
func instrumentCostFreshness(db *gorm.DB) {
	freshnessDB.Store(db)
	freshnessOnce.Do(func() {
		prometheus.MustRegister(costFreshnessCollector{})
	})
}
