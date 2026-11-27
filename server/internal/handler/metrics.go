package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
