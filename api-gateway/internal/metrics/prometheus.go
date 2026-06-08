package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// All metrics are registered at package init time via promauto.
// promauto registers with the default registry automatically —
// no manual Register() calls needed anywhere in the codebase.
//
// Naming convention: <service>_<subsystem>_<name>_<unit>
// This matches the Prometheus community naming standards and makes
// Grafana query autocomplete work cleanly.

var (
	// --- Request metrics ---

	// RequestsTotal counts every request the gateway receives, labelled by
	// HTTP method, path, status code, and user tier. This is the primary
	// traffic volume metric shown in the Grafana overview panel.
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_requests_total",
			Help: "Total number of requests received by the gateway",
		},
		[]string{"method", "path", "status_code", "tier"},
	)

	// RequestDuration measures end-to-end latency for every request.
	// Using a histogram (not a gauge) lets Grafana compute P50/P95/P99.
	// Buckets are tuned for an API gateway: sub-millisecond to multi-second.
	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_request_duration_seconds",
			Help:    "End-to-end request latency in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
		},
		[]string{"method", "path", "tier"},
	)

	// ActiveConnections tracks the number of requests currently in-flight.
	// This gauge rises during load spikes and falls as requests complete.
	// A rising trend that never falls indicates goroutine leaks.
	ActiveConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "gateway_active_connections",
			Help: "Number of requests currently being processed",
		},
	)

	// --- Rate limit metrics ---

	// RateLimitDecisions counts allow/throttle/reject decisions per tier.
	// This is the most visually interesting metric during a load test —
	// Grafana shows a stacked bar chart transitioning from allow → throttle
	// → reject as traffic increases past tier limits.
	RateLimitDecisions = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_ratelimit_decisions_total",
			Help: "Total rate limit decisions broken down by outcome and tier",
		},
		[]string{"decision", "tier"},
	)

	// ThrottleDelay records the actual delay introduced per throttled request.
	// Useful for verifying the throttle mechanism is working as configured.
	ThrottleDelay = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_throttle_delay_seconds",
			Help:    "Artificial delay introduced for throttled requests",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1.0, 2.0},
		},
		[]string{"tier"},
	)

	// --- Auth metrics ---

	// AuthFailures counts authentication failures by reason.
	// Sudden spikes in TOKEN_INVALID may indicate a brute force attempt.
	AuthFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_auth_failures_total",
			Help: "Total authentication failures broken down by reason",
		},
		[]string{"reason"},
	)

	// --- IP blocklist metrics ---

	// BlockedRequests counts requests rejected by the IP blocklist.
	// Visible in Grafana as a separate series so blocked traffic does not
	// inflate rate limit or auth failure counters.
	BlockedRequests = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "gateway_blocked_requests_total",
			Help: "Total requests rejected by the IP blocklist",
		},
	)

	// --- Circuit breaker metrics ---

	// CircuitBreakerState tracks the current state of the breaker as a gauge.
	// 0 = CLOSED (healthy), 1 = OPEN (failing fast), 2 = HALF_OPEN (probing).
	// In Grafana this renders as a status panel: green/red/yellow.
	// When this gauge flips to 1 during a demo it is immediately visible.
	CircuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_circuit_breaker_state",
			Help: "Current circuit breaker state: 0=closed, 1=open, 2=half_open",
		},
		[]string{"backend"},
	)

	// CircuitBreakerTransitions counts how many times the breaker has changed
	// state. Useful for seeing how many times the backend has recovered.
	CircuitBreakerTransitions = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_circuit_breaker_transitions_total",
			Help: "Total circuit breaker state transitions",
		},
		[]string{"backend", "from_state", "to_state"},
	)

	// --- Proxy metrics ---

	// ProxyRequestDuration measures only the backend round-trip time —
	// excludes gateway processing time. Comparing this to RequestDuration
	// shows how much latency the gateway itself adds.
	ProxyRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_proxy_backend_duration_seconds",
			Help:    "Time spent waiting for the backend to respond",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
		},
		[]string{"backend", "status_code"},
	)

	// ProxyErrors counts backend errors that the proxy encounters.
	// Label "type" distinguishes timeout, connection_refused, and 5xx.
	ProxyErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_proxy_errors_total",
			Help: "Total errors encountered while proxying to the backend",
		},
		[]string{"backend", "type"},
	)
)