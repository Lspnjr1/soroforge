package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestMetricsMiddlewareUsesRoutePatterns(t *testing.T) {
	jobs := newJobManager(time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	metrics := newAPIMetrics(jobs)
	router := chi.NewRouter()
	router.Use(metrics.middleware)
	router.Get("/v1/jobs/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/jobs/private-job-id", nil))
	require.Equal(t, http.StatusNotFound, response.Code)

	families, err := metrics.registry.Gather()
	require.NoError(t, err)
	requests := metricFamily(t, families, "soroforge_http_requests_total")
	require.Len(t, requests.Metric, 1)
	require.Equal(t, "/v1/jobs/{id}", labelValue(requests.Metric[0], "route"))
	require.Equal(t, "404", labelValue(requests.Metric[0], "status"))
	require.NotContains(t, labelValue(requests.Metric[0], "route"), "private-job-id")

	latency := metricFamily(t, families, "soroforge_http_request_duration_seconds")
	require.Len(t, latency.Metric, 1)
	require.Equal(t, "/v1/jobs/{id}", labelValue(latency.Metric[0], "route"))

	rateLimited := httptest.NewRecorder()
	router = chi.NewRouter()
	router.Use(metrics.middleware)
	router.Handle("/v1/deploy", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	router.ServeHTTP(rateLimited, httptest.NewRequest(http.MethodPost, "/v1/deploy", nil))
	families, err = metrics.registry.Gather()
	require.NoError(t, err)
	limited := metricFamily(t, families, "soroforge_http_rate_limited_requests_total")
	require.Len(t, limited.Metric, 1)
	require.Equal(t, "/v1/deploy", labelValue(limited.Metric[0], "route"))
	require.Equal(t, float64(1), limited.Metric[0].GetCounter().GetValue())
}

func TestMetricsTrackDeploymentOutcomesAndJobStates(t *testing.T) {
	jobs := newJobManager(time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	metrics := newAPIMetrics(jobs)
	metrics.recordDeployment("deploy", nil)
	metrics.recordDeployment("upgrade", errors.New("failed"))

	releaseAlias := jobs.aliases.acquire("counter")
	started := make(chan struct{})
	finish := make(chan struct{})
	job, err := jobs.submit(jobSpec{
		alias: "counter",
		run: func(_ context.Context) (any, error) {
			close(started)
			<-finish
			return nil, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, JobQueued, job.State)

	queued, err := metrics.registry.Gather()
	require.NoError(t, err)
	require.Equal(t, float64(1), jobGauge(t, queued, JobQueued))
	releaseAlias()
	<-started
	running, err := metrics.registry.Gather()
	require.NoError(t, err)
	require.Equal(t, float64(0), jobGauge(t, running, JobQueued))
	require.Equal(t, float64(1), jobGauge(t, running, JobRunning))
	close(finish)

	deployments, err := metrics.registry.Gather()
	require.NoError(t, err)
	operations := metricFamily(t, deployments, "soroforge_deployments_total")
	require.Len(t, operations.Metric, 2)
	assertCounter(t, operations, "deploy", "success", 1)
	assertCounter(t, operations, "upgrade", "failure", 1)

	// Let the worker transition out of running before the test returns.
	deadline := time.Now().Add(time.Second)
	for jobs.count(JobRunning) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

func metricFamily(t *testing.T, families []*dto.MetricFamily, name string) *dto.MetricFamily {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	t.Fatalf("metric family %q not found", name)
	return nil
}

func labelValue(metric *dto.Metric, name string) string {
	for _, label := range metric.GetLabel() {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

func jobGauge(t *testing.T, families []*dto.MetricFamily, state string) float64 {
	t.Helper()
	family := metricFamily(t, families, "soroforge_async_jobs")
	for _, metric := range family.Metric {
		if labelValue(metric, "state") == state {
			return metric.GetGauge().GetValue()
		}
	}
	t.Fatalf("job gauge state %q not found", state)
	return 0
}

func assertCounter(t *testing.T, family *dto.MetricFamily, operation, outcome string, expected float64) {
	t.Helper()
	for _, metric := range family.Metric {
		if labelValue(metric, "operation") == operation && labelValue(metric, "outcome") == outcome {
			require.Equal(t, expected, metric.GetCounter().GetValue())
			return
		}
	}
	t.Fatalf("deployment counter %s/%s not found", operation, outcome)
}
