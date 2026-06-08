package middleware

import (
	"net/http"
	"strconv"
	"time"

	"api-gateway/internal/metrics"

	"go.uber.org/zap"
)

// Observability is Layer 3 in the middleware chain.
// It runs on every single request and is responsible for two things:
//   1. Emitting Prometheus metrics (request count + latency histogram)
//   2. Writing one structured JSON log line per request via Zap
//
// It must run after RequestID (so the trace ID is in context) and before
// everything else (so it captures the full end-to-end latency including
// auth, rate limiting, and proxy time).
func Observability(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			requestID := GetRequestID(r.Context())

			// Increment active connections gauge — decremented in defer.
			metrics.ActiveConnections.Inc()
			defer metrics.ActiveConnections.Dec()

			// Wrap the ResponseWriter to capture status code and bytes.
			rec := newResponseRecorder(w)

			next.ServeHTTP(rec, r)

			duration := time.Since(start)
			statusCode := strconv.Itoa(rec.statusCode)

			// Extract tier label for Prometheus and logs.
			// Three possible values:
			//   - tier name (free/premium/enterprise) — authenticated user request
			//   - "internal"                          — system routes that bypass auth
			//   - "unauthenticated"                   — auth middleware ran but token was missing or invalid
			tier := resolveTierLabel(r)

			// Prometheus counters and histograms.
			// Path is used as-is — in production you would normalise
			// dynamic segments (/api/v1/records/123 → /api/v1/records/:id)
			// to prevent high cardinality. For this project the paths are
			// static so this is not a concern.
			metrics.RequestsTotal.WithLabelValues(
				r.Method,
				r.URL.Path,
				statusCode,
				tier,
			).Inc()

			metrics.RequestDuration.WithLabelValues(
				r.Method,
				r.URL.Path,
				tier,
			).Observe(duration.Seconds())

			// Structured log line — one per request, feeds Grafana Loki.
			logFn := log.Info
			if rec.statusCode >= 500 {
				logFn = log.Error
			} else if rec.statusCode >= 400 {
				logFn = log.Warn
			}

			logFn("request completed",
				zap.String("request_id", requestID),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("user_agent", r.UserAgent()),
				zap.Int("status_code", rec.statusCode),
				zap.Int("bytes_written", rec.bytesWritten),
				zap.Duration("latency", duration),
				zap.Int64("latency_ms", duration.Milliseconds()),
				zap.String("tier", tier),
				zap.String("x_forwarded_for", r.Header.Get("X-Forwarded-For")),
				zap.String("x_trace_id", requestID),
			)
		})
	}
}

// resolveTierLabel determines the correct tier label for a request.
// Internal routes like /healthz, /readyz, and /metrics bypass the auth
// middleware entirely — labelling them "unauthenticated" is misleading
// because they are not user requests. They get their own "internal" label
// so Grafana panels filtering on tier do not mix system traffic with
// user traffic.
func resolveTierLabel(r *http.Request) string {
	switch r.URL.Path {
	case "/healthz", "/readyz", "/metrics":
		return "internal"
	}

	// Admin routes also bypass JWT auth — they use the static admin token.
	if len(r.URL.Path) >= 6 && r.URL.Path[:6] == "/admin" {
		return "internal"
	}

	if userCtx := GetUserContext(r.Context()); userCtx != nil {
		return userCtx.Tier.Name
	}

	// Auth middleware ran but the token was missing, expired, or invalid.
	return "unauthenticated"
}