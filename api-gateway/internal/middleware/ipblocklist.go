package middleware

import (
	"net/http"

	"api-gateway/internal/ipblocklist"
	"api-gateway/internal/metrics"
	"api-gateway/pkg/response"

	"go.uber.org/zap"
)

// IPBlocklist is Layer 4 in the middleware chain — runs after observability
// has logged the request but before auth performs JWT verification.
//
// Rejecting banned IPs before auth means:
// - No JWT cryptography wasted on banned clients
// - No rate limit counter incremented for banned clients
// - Minimum CPU cost per banned request (one Redis SET lookup)
func IPBlocklist(bl *ipblocklist.Blocklist, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := GetRequestID(r.Context())
			clientIP := ipblocklist.ExtractIP(r)

			blocked, err := bl.IsBlocked(r.Context(), clientIP)
			if err != nil {
				// Redis error already logged inside IsBlocked — fail open
				next.ServeHTTP(w, r)
				return
			}

			if blocked {
				log.Warn("blocked ip rejected",
					zap.String("request_id", requestID),
					zap.String("client_ip", clientIP),
					zap.String("path", r.URL.Path),
				)

				// Increment the Prometheus counter so Grafana can show blocked traffic.
				metrics.BlockedRequests.Inc()

				response.Fail(w, http.StatusForbidden,
					"IP_BLOCKED",
					"your IP address has been blocked",
				)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}