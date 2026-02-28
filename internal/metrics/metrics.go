// Package metrics provides a custom Prometheus registry with all application
// metric definitions for dns-he-net-automation.
//
// A custom registry (not prometheus.DefaultRegisterer) is used to avoid test
// panics on duplicate registration and to keep metrics isolated from the
// global default registry.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds all application metrics for dns-he-net-automation.
// All metrics use the "dnshe" namespace with type-appropriate subsystems.
type Registry struct {
	// reg is the underlying custom Prometheus registry. Unexported — access via Handler().
	reg *prometheus.Registry

	// HTTP layer metrics (OBS-01)
	// dnshe_http_requests_total — labelled by method, route pattern, and status code.
	HTTPRequestsTotal *prometheus.CounterVec
	// dnshe_http_request_duration_seconds — labelled by method and route pattern.
	// Buckets extended to 30s to cover DNS scraping operations (2–10s typical).
	HTTPRequestDuration *prometheus.HistogramVec

	// Browser operation metrics (OBS-01)
	// dnshe_browser_operations_total — labelled by op_type and result.
	BrowserOpsTotal *prometheus.CounterVec
	// dnshe_browser_operation_duration_seconds — labelled by op_type.
	BrowserOpDuration *prometheus.HistogramVec

	// Session state (OBS-01)
	// dnshe_browser_active_sessions — number of live browser sessions.
	ActiveSessions prometheus.Gauge

	// Per-account queue depth (OBS-01)
	// dnshe_browser_queue_depth — requests waiting for the per-account browser mutex.
	QueueDepth *prometheus.GaugeVec

	// Application error counts (OBS-01)
	// dnshe_app_errors_total — labelled by error_type.
	ErrorsTotal *prometheus.CounterVec

	// Sync operation counts (OBS-01 open question 4)
	// dnshe_sync_operations_total — labelled by op_type (add/update/delete) and result (ok/error).
	SyncOpsTotal *prometheus.CounterVec
}

// NewRegistry creates a fresh custom Prometheus registry with all application
// metrics pre-registered. Each call returns an independent registry — safe to
// call multiple times in tests without duplicate-registration panics.
func NewRegistry() *Registry {
	// Use a custom registry, NOT prometheus.DefaultRegisterer.
	// This prevents test panics when NewRegistry is called more than once (Pitfall 2).
	reg := prometheus.NewRegistry()

	// Register standard Go process and runtime metrics on the custom registry.
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// promauto.With scopes all metric registrations to the custom registry.
	// Never call promauto.NewCounterVec() directly — that uses DefaultRegisterer.
	f := promauto.With(reg)

	return &Registry{
		reg: reg,

		// HTTP layer — subsystem "http"
		HTTPRequestsTotal: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: "dnshe",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total HTTP requests by method, route pattern, and status code.",
		}, []string{"method", "route", "status"}),

		HTTPRequestDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "dnshe",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds by method and route pattern.",
			// Extended buckets: DNS scraping operations typically take 2–10s.
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 15, 30},
		}, []string{"method", "route"}),

		// Browser operations — subsystem "browser"
		BrowserOpsTotal: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: "dnshe",
			Subsystem: "browser",
			Name:      "operations_total",
			Help:      "Total browser operations by type and result.",
		}, []string{"op_type", "result"}),

		BrowserOpDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "dnshe",
			Subsystem: "browser",
			Name:      "operation_duration_seconds",
			Help:      "Browser operation duration in seconds by type.",
			// Buckets start at 0.5s — browser ops are never sub-millisecond.
			Buckets: []float64{.5, 1, 2.5, 5, 10, 15, 30},
		}, []string{"op_type"}),

		ActiveSessions: f.NewGauge(prometheus.GaugeOpts{
			Namespace: "dnshe",
			Subsystem: "browser",
			Name:      "active_sessions",
			Help:      "Number of active browser sessions.",
		}),

		QueueDepth: f.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "dnshe",
			Subsystem: "browser",
			Name:      "queue_depth",
			Help:      "Number of requests waiting for the per-account browser mutex.",
		}, []string{"account_id"}),

		// Application errors — subsystem "app"
		ErrorsTotal: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: "dnshe",
			Subsystem: "app",
			Name:      "errors_total",
			Help:      "Total errors by type.",
		}, []string{"error_type"}),

		// Sync operations — subsystem "sync"
		SyncOpsTotal: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: "dnshe",
			Subsystem: "sync",
			Name:      "operations_total",
			Help:      "Total sync operations by type (add/update/delete) and result (ok/error).",
		}, []string{"op_type", "result"}),
	}
}

// Handler returns the HTTP handler that serves the Prometheus text exposition
// format at GET /metrics. Register this at the root router level, not inside
// the /api/v1 group — Prometheus scrapers must not be behind auth middleware
// (Pitfall 4 from research).
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
}
