package middleware

import (
	"errors"
	"net/http"

	"api-gateway/internal/circuitbreaker"
	"api-gateway/pkg/response"

	"go.uber.org/zap"
)

// CircuitBreaker is Layer 7 in the middleware chain — the last gate before
// the reverse proxy forwards to the backend.
//
// It consults the breaker's state machine on every request:
//   CLOSED    → allow through, record outcome after response
//   OPEN      → fail fast with 503, no backend contact
//   HALF_OPEN → allow one probe, all others get 503
func CircuitBreaker(breaker *circuitbreaker.Breaker, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := GetRequestID(r.Context())

			if err := breaker.Allow(); err != nil {
				retryAfter := circuitbreaker.FormatRetryAfter(breaker.RetryAfter())

				log.Warn("circuit breaker rejected request",
					zap.String("request_id", requestID),
					zap.String("state", breaker.CurrentState().String()),
					zap.String("retry_after", retryAfter),
					zap.Error(err),
				)

				w.Header().Set("Retry-After", retryAfter)

				code := "CIRCUIT_OPEN"
				msg := "backend is temporarily unavailable — please retry shortly"
				if errors.Is(err, circuitbreaker.ErrProbeInFlight) {
					msg = "circuit breaker is recovering — probe already in flight"
				}

				response.Fail(w, http.StatusServiceUnavailable, code, msg)
				return
			}

			// Wrap the ResponseWriter to capture the status code so we
			// can tell RecordSuccess from RecordFailure after the proxy runs.
			rec := newResponseRecorder(w)
			next.ServeHTTP(rec, r)

			// Classify the backend response.
			// 5xx responses and client-side timeouts are failures.
			// Everything else (including 4xx) is a success from the
			// circuit breaker's perspective — the backend is reachable.
			if rec.statusCode >= 500 {
				breaker.RecordFailure()
				log.Debug("circuit breaker: recorded failure",
					zap.String("request_id", requestID),
					zap.Int("status_code", rec.statusCode),
					zap.String("breaker_state", breaker.CurrentState().String()),
				)
			} else {
				breaker.RecordSuccess()
			}
		})
	}
}