package middleware

import (
	"net/http"
	"strconv"
	"time"

	"api-gateway/internal/metrics"
	"api-gateway/internal/ratelimiter"
	"api-gateway/pkg/response"

	"go.uber.org/zap"
)

// RateLimit is Layer 6 in the middleware chain — runs after auth has
// populated the UserContext so we know the user's tier and RPM limit.
//
// Three outcomes:
//   DecisionAllow    — forward immediately
//   DecisionThrottle — introduce configured delay then forward
//   DecisionReject   — return 429, short-circuit
//
// Rate limit headers are always injected on every response so clients
// can implement backoff logic correctly.
func RateLimit(limiter *ratelimiter.Limiter, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := GetRequestID(r.Context())

			userCtx := GetUserContext(r.Context())
			if userCtx == nil {
				// Auth middleware did not run or failed — this should not
				// happen in a correctly ordered chain, but defend anyway.
				log.Error("rate limit middleware: no user context found",
					zap.String("request_id", requestID),
				)
				response.Fail(w, http.StatusInternalServerError,
					"INTERNAL_ERROR", "authentication context missing")
				return
			}

			result, err := limiter.Evaluate(
				r.Context(),
				userCtx.UserID,
				userCtx.Tier.RequestsPerMinute,
				userCtx.ThrottleThreshold(),
			)
			if err != nil {
				log.Error("rate limiter evaluation error",
					zap.String("request_id", requestID),
					zap.String("user_id", userCtx.UserID),
					zap.Error(err),
				)
				// Fail open — do not block the user on internal errors
				next.ServeHTTP(w, r)
				return
			}

			// Always inject rate limit headers regardless of decision.
			// Clients use these to implement intelligent backoff.
			injectRateLimitHeaders(w, result)

			// Record the decision in Prometheus for Grafana dashboard.
			metrics.RateLimitDecisions.WithLabelValues(
				result.Decision.String(),
				userCtx.Tier.Name,
			).Inc()

			switch result.Decision {
			case ratelimiter.DecisionReject:
				log.Warn("request rejected — rate limit exceeded",
					zap.String("request_id", requestID),
					zap.String("user_id", userCtx.UserID),
					zap.String("tier", userCtx.Tier.Name),
					zap.Int("count", result.CurrentCount),
					zap.Int("limit", result.Limit),
				)
				response.Fail(w, http.StatusTooManyRequests,
					"RATE_LIMIT_EXCEEDED",
					"you have exceeded your request rate limit",
				)
				return

			case ratelimiter.DecisionThrottle:
				log.Debug("request throttled — introducing delay",
					zap.String("request_id", requestID),
					zap.String("user_id", userCtx.UserID),
					zap.Int64("delay_ms", result.ThrottleDelayMs),
				)
				// Context-aware delay — wakes up if client disconnects.
				// Never use time.Sleep in a hot path.
				select {
				case <-time.After(time.Duration(result.ThrottleDelayMs) * time.Millisecond):
				case <-r.Context().Done():
					return
				}
				next.ServeHTTP(w, r)

			default:
				// DecisionAllow — forward immediately
				next.ServeHTTP(w, r)
			}
		})
	}
}

// injectRateLimitHeaders writes standard rate limit headers to the response.
// These headers are the industry standard (used by GitHub, Stripe, etc.)
// and allow clients to implement proper backoff without guessing.
func injectRateLimitHeaders(w http.ResponseWriter, result *ratelimiter.Result) {
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(result.Limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(
		time.Now().Add(time.Duration(result.ResetAfterMs)*time.Millisecond).Unix(), 10,
	))
	if result.Decision == ratelimiter.DecisionThrottle {
		w.Header().Set("X-RateLimit-Throttled", "true")
		w.Header().Set("Retry-After", strconv.FormatInt(result.ThrottleDelayMs/1000+1, 10))
	}
}