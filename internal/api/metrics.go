package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// apiMetrics owns the bounded-cardinality metrics exposed by GET /metrics.
// A private registry keeps API metrics separate from global collectors and
// makes multiple API routers safe to construct in one process.
type apiMetrics struct {
	registry       *prometheus.Registry
	requests       *prometheus.CounterVec
	requestLatency *prometheus.HistogramVec
	deployments    *prometheus.CounterVec
	rateLimited    *prometheus.CounterVec
	handler        http.Handler
}

func newAPIMetrics(jobs *jobManager) *apiMetrics {
	registry := prometheus.NewRegistry()
	m := &apiMetrics{
		registry: registry,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "soroforge_http_requests_total",
			Help: "Total HTTP requests handled by the SoroForge API.",
		}, []string{"method", "route", "status"}),
		requestLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "soroforge_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds by method and route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		deployments: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "soroforge_deployments_total",
			Help: "Total deploy and upgrade operations by outcome.",
		}, []string{"operation", "outcome"}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "soroforge_http_rate_limited_requests_total",
			Help: "Total API requests rejected by the rate limiter.",
		}, []string{"route"}),
	}

	registry.MustRegister(m.requests, m.requestLatency, m.deployments, m.rateLimited)
	for _, state := range []string{JobQueued, JobRunning} {
		state := state
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "soroforge_async_jobs",
			Help:        "Current asynchronous jobs by state.",
			ConstLabels: prometheus.Labels{"state": state},
		}, func() float64 { return float64(jobs.count(state)) }))
	}
	m.handler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return m
}

func (m *apiMetrics) serveHTTP(w http.ResponseWriter, r *http.Request) {
	m.handler.ServeHTTP(w, r)
}

// middleware records request counts and durations using chi route patterns.
// Requests that match no route share the fixed "unmatched" label; raw paths
// and identifiers are never used as metric labels.
func (m *apiMetrics) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r)

		status := wrapped.Status()
		if status == 0 {
			status = http.StatusOK
		}
		route := metricRoute(r)
		m.requests.WithLabelValues(r.Method, route, strconv.Itoa(status)).Inc()
		m.requestLatency.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
		if status == http.StatusTooManyRequests && route != "unmatched" {
			m.rateLimited.WithLabelValues(route).Inc()
		}
	})
}

func metricRoute(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if pattern := rctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return "unmatched"
}

func (m *apiMetrics) recordDeployment(operation string, err error) {
	outcome := "success"
	if err != nil {
		outcome = "failure"
	}
	m.deployments.WithLabelValues(operation, outcome).Inc()
}
