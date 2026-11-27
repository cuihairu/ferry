package handler

import (
	"database/sql"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/gorm"
)

// Server metrics layer (F-1801+): Prometheus collectors registered on the
// default registry, served at GET /admin-authorized /metrics. Route labels
// use the gin route template (e.g. /api/nodes/:id/config), so cardinality is
// bounded by the route table; unmatched paths collapse into "unmatched".
var (
	apiRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ferry_api_requests_total",
		Help: "Server API requests by method, route template and status code.",
	}, []string{"method", "route", "code"})
	apiLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ferry_api_request_seconds",
		Help:    "Server API request latency in seconds by method and route template.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
)

// metricsMiddleware counts every /api request and its latency. Mounted
// outermost on the api group so auth failures and rate-limit rejections are
// counted too (status code label); the agent websocket and public /sub
// surfaces are outside this group by design (the ws connection lives for the
// session, which would poison the latency histogram).
func metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		apiRequests.WithLabelValues(c.Request.Method, route, strconv.Itoa(c.Writer.Status())).Inc()
		apiLatency.WithLabelValues(c.Request.Method, route).Observe(time.Since(start).Seconds())
	}
}

// metricsHandler exposes the default registry in Prometheus text format.
// Mounted behind the admin auth middleware: metric cardinality leaks route
// usage shape, so the endpoint is not public.
func metricsHandler() gin.HandlerFunc {
	return gin.WrapH(promhttp.Handler())
}

// ---- Database metrics (F-1802): pool gauges scraped from sql.DBStats plus
// statement latency timed on the GORM callback chain. ----

var (
	dbQuerySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ferry_db_query_seconds",
		Help:    "Database statement latency in seconds by operation (create/query/update/delete; raw reads report under query).",
		Buckets: prometheus.DefBuckets,
	}, []string{"op"})
	dbQueryErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ferry_db_query_errors_total",
		Help: "Database statement errors by operation; routine record-not-found misses are not counted.",
	}, []string{"op"})
)

const dbMetricsStart = "ferry:metrics:start"

// instrumentDB hooks the GORM callback chain to time every statement class
// and count its failures.
func instrumentDB(db *gorm.DB) {
	start := func(tx *gorm.DB) { tx.Set(dbMetricsStart, time.Now()) }
	after := func(op string) func(*gorm.DB) {
		return func(tx *gorm.DB) {
			if v, ok := tx.Get(dbMetricsStart); ok {
				if t, ok := v.(time.Time); ok {
					dbQuerySeconds.WithLabelValues(op).Observe(time.Since(t).Seconds())
				}
			}
			if tx.Error != nil && !errors.Is(tx.Error, gorm.ErrRecordNotFound) {
				dbQueryErrors.WithLabelValues(op).Inc()
			}
		}
	}
	q := db.Callback().Query()
	q.Before("gorm:query").Register("ferry:metrics:before:query", start)
	q.After("gorm:query").Register("ferry:metrics:after:query", after("query"))
	c := db.Callback().Create()
	c.Before("gorm:create").Register("ferry:metrics:before:create", start)
	c.After("gorm:create").Register("ferry:metrics:after:create", after("create"))
	u := db.Callback().Update()
	u.Before("gorm:update").Register("ferry:metrics:before:update", start)
	u.After("gorm:update").Register("ferry:metrics:after:update", after("update"))
	d := db.Callback().Delete()
	d.Before("gorm:delete").Register("ferry:metrics:before:delete", start)
	d.After("gorm:delete").Register("ferry:metrics:after:delete", after("delete"))
}

// dbHandle points at the live *sql.DB so the stats gauges answer scrapes with
// current pool state (many test routers boot in one process; last wins — in
// production there is exactly one).
var dbHandle atomic.Pointer[sql.DB]

var dbStatsOnce sync.Once

type dbStatsCollector struct{}

var dbStatsDescs = []*prometheus.Desc{
	prometheus.NewDesc("ferry_db_open_connections", "Currently open database connections.", nil, nil),
	prometheus.NewDesc("ferry_db_in_use_connections", "Database connections currently in use.", nil, nil),
	prometheus.NewDesc("ferry_db_idle_connections", "Idle database connections.", nil, nil),
	prometheus.NewDesc("ferry_db_connection_waits_total", "Total number of connections waited for.", nil, nil),
	prometheus.NewDesc("ferry_db_connection_wait_seconds_total", "Total time blocked waiting for a connection, in seconds.", nil, nil),
}

func (dbStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range dbStatsDescs {
		ch <- d
	}
}

func (dbStatsCollector) Collect(ch chan<- prometheus.Metric) {
	h := dbHandle.Load()
	if h == nil {
		return
	}
	s := h.Stats()
	vals := []struct {
		desc  *prometheus.Desc
		typ   prometheus.ValueType
		value float64
	}{
		{dbStatsDescs[0], prometheus.GaugeValue, float64(s.OpenConnections)},
		{dbStatsDescs[1], prometheus.GaugeValue, float64(s.InUse)},
		{dbStatsDescs[2], prometheus.GaugeValue, float64(s.Idle)},
		{dbStatsDescs[3], prometheus.CounterValue, float64(s.WaitCount)},
		{dbStatsDescs[4], prometheus.CounterValue, s.WaitDuration.Seconds()},
	}
	for _, v := range vals {
		ch <- prometheus.MustNewConstMetric(v.desc, v.typ, v.value)
	}
}

// instrumentDBStats registers the pool gauges once and points them at this
// process's *sql.DB.
func instrumentDBStats(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		dbHandle.Store(sqlDB)
		dbStatsOnce.Do(func() { prometheus.MustRegister(dbStatsCollector{}) })
	}
}
